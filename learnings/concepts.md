# Learnings & Knowledge — my-non-relational

Conceptos teóricos, trade-offs, ejemplos y conexiones con sistemas reales para cada fase del proyecto.
Esta guía acompaña el código: mientras el README describe *qué* se implementa, este documento explica *por qué* funciona y *qué nos enseña*.

---

## Índice

1. [Hash Map — Open Addressing](#1-hash-map--open-addressing)
2. [Robin Hood Hashing](#2-robin-hood-hashing)
3. [WAL + CRC32 — Durabilidad y Detección de Corrupción](#3-wal--crc32--durabilidad-y-detección-de-corrupción)
4. [Bloom Filter](#4-bloom-filter)
5. [Sorted Slice + Binary Search](#5-sorted-slice--binary-search)
6. [Min-Heap y TopK](#6-min-heap-y-topk)
7. [BST Naïve](#7-bst-naïve)
8. [AVL Tree](#8-avl-tree)
9. [Skip List](#9-skip-list)
10. [Concurrencia: RWMutex y Atomics](#10-concurrencia-rwmutex-y-atomics)
11. [HyperLogLog](#11-hyperloglog)
12. [Count-Min Sketch](#12-count-min-sketch)
13. [Compaction y Rename Atómico](#13-compaction-y-rename-atómico)
14. [SSTables y K-way Merge](#14-sstables-y-k-way-merge)
15. [El LSM Tree — El Proyecto Completo](#15-el-lsm-tree--el-proyecto-completo)
16. [Ring Buffer — Replicación](#16-ring-buffer--replicación)
17. [Tabla de Trade-offs General](#17-tabla-de-trade-offs-general)

---

## 1. Hash Map — Open Addressing

### Concepto

Un Hash Map transforma una clave (string) en un índice de array usando una función hash. El objetivo: acceso O(1) amortizado sin recorrer toda la estructura.

Hay dos familias principales de implementación:

| Estrategia | Cómo maneja colisiones | Memoria | Cache |
|---|---|---|---|
| **Chaining** | Lista enlazada por slot | O(N) + punteros | Malo (saltos de puntero) |
| **Open Addressing** | Probing secuencial en el array | O(capacity) | Bueno (array contiguo) |

Este proyecto usa **open addressing con linear probing**: si el slot está ocupado, avanzamos al siguiente.

### Función hash: FNV-1a

```
offset_basis = 14695981039346656037
prime        = 1099511628211

hash = offset_basis
for each byte b in key:
    hash = hash XOR b
    hash = hash * prime
```

FNV-1a (Fowler–Noll–Vo) tiene buen avalanche: un solo bit de cambio en la entrada cambia ~50% de los bits de salida. Es rápida y sin dependencias externas.

### Load Factor

```
load_factor = count / capacity
```

Con load_factor alto, las colisiones aumentan exponencialmente (birthday paradox). A 0.7, el número promedio de probes para un lookup es ~2.5. A 0.9, sube a ~5.5. Por eso rehashear antes de superar 0.7.

```
Probes promedio (fórmula de Knuth para linear probing):
  Hit:  (1 + 1/(1 - α)²) / 2
  Miss: (1 + 1/(1 - α)) / 2
  donde α = load_factor
```

### Rehash

Cuando el load factor supera el umbral, se duplica la capacidad y se reinsertan todos los elementos. La capacidad siempre es potencia de 2 para que `hash % capacity` sea un AND a nivel de bits: más rápido que módulo en hardware.

```
slot = hash & (capacity - 1)  // equivalente a hash % capacity, pero más rápido
```

### Trade-offs

| Decisión | Ganamos | Perdemos |
|---|---|---|
| Open addressing vs chaining | Localidad de caché; un solo alloc | Degradación con alta carga |
| Linear probing vs quadratic | Implementación simple | Clustering primario (huecos grandes) |
| Load factor 0.7 | Balance razonable entre velocidad y memoria | ~30% capacidad desperdiciada |
| Potencia de 2 para capacity | `hash & (cap-1)` en vez de `%` | Rehash siempre duplica (puede ser grande) |

### Conexión con sistemas reales

- Go's `map` built-in: usa Swiss Tables (SIMD para lookup en grupos de 8 slots).
- Java's `HashMap`: chaining con árboles rojo-negro cuando la cadena supera 8 (Java 8+).
- Python's `dict`: open addressing con perturbation para reducir clustering.

---

## 2. Robin Hood Hashing

### El problema: tombstones

Con open addressing estándar, `Delete` deja una marca muerta (tombstone). Sin esto, al buscar una clave que fue desplazada durante una inserción, podríamos detenernos en el hueco antes de encontrarla.

```
Antes de delete:
slot 3: A (ideal=3, dib=0)
slot 4: B (ideal=3, dib=1)  ← colisión, desplazado
slot 5: C (ideal=5, dib=0)

Delete A → ¿ponemos el slot 3 como vacío?
Get B → busca en slot 3 (ideal), slot 3 vacío → ¡retorna "no encontrado"! ← BUG
```

Solución tombstone: marcar slot 3 como "muerto pero no vacío". `Get` salta tombstones.

El problema: con muchas deletes, la tabla se llena de tombstones y cada lookup los recorre todos. Performance degrada hasta el próximo rehash.

### Robin Hood: DIB como invariante

**DIB** (Distance from Initial Bucket): cuántos slots se desplazó un elemento desde su slot ideal.

```
Elemento X con ideal=5, actualmente en slot 8 → DIB = 3
```

**Invariante de Robin Hood**: en cualquier slot, el elemento tiene DIB ≥ todos los elementos que vienen antes en su probe chain. Dicho de otro modo: los elementos con más "suerte" (DIB bajo) no pueden bloquear a los elementos con menos (DIB alto).

**Inserción**:
```
Queremos insertar X con DIB=0 (en su slot ideal):
  slot[ideal] tiene Y con DIB=3 → X tiene menos (0 < 3)
  → Y cede el slot a X (el rico da al pobre: Robin Hood)
  → Y continúa buscando desde el siguiente slot con DIB=4
```

**Early exit en lookup**: si al buscar X encontramos un elemento con DIB < nuestra probe distance actual, X no puede estar aquí. ¿Por qué? Porque Robin Hood garantiza que si X hubiera sido insertado, habría desplazado a este elemento (tenía más DIB). Como no lo desplazó, X no existe en la tabla.

```
Buscamos X (ideal=5), llegamos a slot 8 con DIB=3:
Si encontramos Y con DIB=2 → X habría tenido DIB=3 aquí
Robin Hood habría puesto X en lugar de Y (3 > 2)
Como Y sigue aquí con DIB=2, X nunca fue insertado.
→ retornar "no encontrado"
```

### Backward Shift Deletion

Eliminar sin tombstones: después de vaciar el slot, desplazar los siguientes elementos hacia atrás hasta encontrar un slot vacío o un elemento en su slot ideal (DIB=0).

```
Antes:
slot 5: A (dib=0)
slot 6: B (dib=2)  ← ideal=4
slot 7: C (dib=3)  ← ideal=4
slot 8: (vacío)

Delete B (slot 6):
  Vaciar slot 6
  C tiene dib=3 > 0 → mover C a slot 6, dib=2
  slot 7 vacío → fin

Después:
slot 5: A (dib=0)
slot 6: C (dib=2)  ← ideal=4
slot 7: (vacío)
slot 8: (vacío)
```

### Trade-offs

| Aspecto | Tombstone | Robin Hood + Backward Shift |
|---|---|---|
| Delete costo | O(1) | O(k) donde k = longitud del chain |
| Lookup post-delete masivo | Degrada (recorre tombstones) | Constante (early exit) |
| Memoria extra | 1 bit por slot para el estado | Entero para DIB |
| Varianza de DIB | Alta (sin control) | Mínima (invariante Robin Hood) |
| Complexity del código | Baja | Media |

**Cuándo Robin Hood gana**: workloads con delete masivo (caché LRU, índices con muchas actualizaciones). Es la estrategia de `std::collections::HashMap` en Rust y de varios sistemas de bases de datos.

### Visualización

```
Tabla con tombstones después de 4,500 deletes sobre 5,000 inserts:
[T][T][A][T][T][T][T][B][T][T][T][T][C][T][T][T]
Get X → debe saltar 12 tombstones antes de encontrar vacío real

Tabla Robin Hood (backward shift):
[A][ ][ ][ ][B][ ][ ][ ][C][ ][ ][ ][ ][ ][ ][ ]
Get X → early exit en 1-2 probes
```

---

## 3. WAL + CRC32 — Durabilidad y Detección de Corrupción

### Write-Ahead Log (WAL)

Un WAL es el mecanismo de durabilidad más sencillo y robusto conocido en bases de datos: antes de modificar cualquier estructura, escribir la intención al log. En crash, releer el log para reconstruir el estado.

**Propiedades fundamentales**:
- **Append-only**: cada escritura es un nuevo registro al final. Nunca se sobreescribe.
- **Durabilidad por `fsync`**: el SO puede tener el dato en buffer. `fsync()` fuerza la escritura a hardware antes de retornar. Esto es ~100x más lento que escribir al buffer.
- **Recovery por replay**: leer el log de inicio a fin. El último registro por `_id` gana. Idempotente.

### Formato de registro

```
┌──────────┬──────────┬──────────┬──────────────────────────┐
│ 4 bytes  │ 4 bytes  │ 4 bytes  │ N bytes                  │
│  size    │  type    │  crc32   │  payload (JSON/MsgPack)   │
│ uint32LE │ uint32LE │ uint32LE │                           │
└──────────┴──────────┴──────────┴──────────────────────────┘
```

El campo `size` permite saltar al siguiente registro sin parsear el payload. El `crc32` detecta corrupción.

### CRC32: checksums

CRC32 (Cyclic Redundancy Check) calcula un valor de 32 bits que actúa como "huella" del payload. Si un solo bit del payload cambia, el CRC cambia con probabilidad 1 - 2^(-32) ≈ 99.9999999%.

```
crc = CRC32(payload_bytes)
```

**¿Qué detecta?**
- Bit flips en disco (radiación cósmica, magnetismo, fallo de celda en SSD)
- Escrituras parciales (crash durante Append: los últimos bytes son basura)
- Errores de transmisión (si el WAL se copia por red)

**¿Qué NO detecta?**
- Corrupción sistemática que preserva el CRC (extremadamente improbable, ~1 en 4 billones)
- Corrupción del propio campo CRC (se puede mitigar con CRC del header completo)

**Recovery con CRC**:
```
for each record in WAL:
    read header (size, type, crc)
    read payload (size bytes)
    if CRC32(payload) != crc:
        log warning "registro corrupto en offset X"
        break  ← fin del replay (no continuar; datos subsiguientes son inciertos)
    apply(type, payload)
```

### El dilema de `fsync`

```
Sin fsync: 100,000 writes/sec — datos en buffer del SO → pérdida en crash
Con fsync:  1,000 writes/sec — datos confirmados en disco → durabilidad real

Opciones intermedias:
  - Group commit: acumular N writes y hacer 1 fsync → throughput sin sacrificar durabilidad
  - WAL asíncrono: escribir sin fsync, replicar a N follower → durabilidad distribuida
  - O_DIRECT: evitar el buffer del SO completamente
```

PostgreSQL usa fsync por default. SQLite tiene modos WAL con sync configurable. RocksDB ofrece `sync=false` para writes de alto throughput con durabilidad opcional.

### Conexiones con sistemas reales

- **PostgreSQL WAL**: mismo concepto. El WAL de PG permite point-in-time recovery (PITR) y replicación streaming.
- **MySQL binlog**: WAL para replicación. Separado del InnoDB redo log.
- **SQLite WAL mode**: permite lectores concurrentes mientras se escribe.
- **Kafka**: es esencialmente un WAL distribuido para mensajería.

### El WAL en otros sistemas — tabla de equivalencias

> **WAL** = el mecanismo por el que un sistema registra qué va a hacer antes de hacerlo, para poder recuperarse de un crash.

| Sistema | Nombre del WAL | Ubicación | Frecuencia de sync | Limpieza |
|---|---|---|---|---|
| **Este proyecto** | `data/data.log` | único fichero de datos | `fsync` por operación | Compaction manual (Fase 9) |
| **PostgreSQL** | WAL / `pg_wal/` | separado de los heap files | por commit (configurable) | Automática post-checkpoint |
| **MongoDB** (WiredTiger) | Journal / `journal/WiredTigerLog.*` | separado de los `.wt` files | cada 100ms por defecto (`j:true` para inmediato) | Automática post-checkpoint (cada 60s) |
| **MySQL** (InnoDB) | Redo log / `ib_logfile*` | separado del tablespace | por commit (configurable con `innodb_flush_log_at_trx_commit`) | Reutilizado en circular, no se borra |
| **SQLite** | WAL file / `db.sqlite-wal` | junto al fichero de base de datos | checkpoint configurable | `PRAGMA wal_checkpoint` o automático |
| **RocksDB** | WAL / `*.log` en `db/` | separado de SSTables | configurable (`sync=false/true`) | Automática cuando MemTable se flushea a SSTable |
| **Kafka** | Commit log / segmentos en `logs/` | por partición | configurable (`log.flush.interval.ms`) | Retención por tiempo o tamaño (`log.retention.*`) |
| **Redis** (AOF) | AOF / `appendonly.aof` | fichero único | `always`, `everysec`, o `no` | `BGREWRITEAOF` (compacta el AOF) |

#### Tres patrones que se repiten en todos

```
1. Append-only:  todos escriben al final, nunca sobreescriben el log
2. Checkpoint:   la mayoría genera snapshots periódicos para acotar el replay
3. Limpieza:     el WAL se puede borrar/compactar solo DESPUÉS de confirmar
                 que el checkpoint lo cubre (equivalente a nuestra Compaction)
```

#### El equivalente al VACUUM de PostgreSQL

`VACUUM` en PostgreSQL no borra el WAL — actúa sobre los **heap files** (tablas), eliminando versiones muertas de filas (dead tuples) que dejó el MVCC. El WAL se limpia por separado.

En este proyecto no hay heap files separados: el WAL es el único almacenamiento. Por eso nuestra **Compaction (Fase 9)** es la operación que más se parece al VACUUM: elimina entradas muertas y deja solo el estado vivo. La diferencia es que Postgres puede hacer VACUUM sin bloquear; aquí necesitamos lock exclusivo.

```
PostgreSQL:       WAL (recovery/repl) + heap files (datos) + VACUUM (limpia heap)
Este proyecto:    WAL (recovery + datos) + Compaction (limpia el propio WAL)
```

---

## 4. Bloom Filter

### El problema

Antes de buscar un ID en disco (binary search + ReadAt), queremos saber si vale la pena. En sistemas de producción, >90% de los `Get` son para IDs que no existen (scans, health checks, cache misses). Cada uno de estos hace un binary search inútil.

### Cómo funciona

Un Bloom Filter es un array de M bits, inicializado a 0. Se usan K funciones hash independientes.

**Add(key)**:
```
para i en 0..K:
    bit = h_i(key) % M
    bits[bit] = 1
```

**MayContain(key)**:
```
para i en 0..K:
    bit = h_i(key) % M
    si bits[bit] == 0:
        return false  ← definitivamente NO está
return true  ← probablemente sí está
```

**Garantía**: los falsos negativos son imposibles. Si `Add(x)` fue llamado, `MayContain(x)` siempre retorna `true`. Los falsos positivos son posibles: bits de otras claves pueden coincidir.

### Probabilidad de falso positivo

Con M bits y K funciones hash para N elementos:

```
P(falso positivo) ≈ (1 - e^(-KN/M))^K

Óptimo K = (M/N) * ln(2)

Con 10 bits/elemento y K≈7:
P ≈ (1 - e^(-7*N/10N))^7 = (1 - e^(-0.7))^7 ≈ 0.008 = 0.8%
```

### Parámetros en este proyecto

```go
// Para N=100,000 docs con FP rate = 1%:
M = ceil(-N * ln(fp) / ln(2)²) ≈ 958,506 bits ≈ 117 KB
K = (M/N) * ln(2) ≈ 7 hash functions
```

### Double Hashing (Kirsch-Mitzenmacher)

En vez de K funciones hash independientes (costosas de computar), usamos 2 y simulamos K:

```
h_i(key) = h1(key) + i * h2(key)   para i = 0, 1, ..., K-1
```

Esto es matemáticamente equivalente a K funciones independientes para propósitos del Bloom Filter, con solo 2 cómputos reales.

```
h1 = FNV-1a(key)
h2 = DJB2(key) | 1   ← OR 1 para garantizar h2 impar (cubre todos los bits)
```

### Trade-offs

| Aspecto | Con Bloom Filter | Sin Bloom Filter |
|---|---|---|
| Lookup de ID inexistente | O(1) en 99.2% casos | O(log N) siempre |
| RAM extra | ~10 bits/doc (~1.2MB por 1M docs) | 0 |
| Falsos positivos | 0.8% → binary search innecesario | N/A |
| Delete | Imposible sin reconstruir | N/A (no es el índice primario) |
| Después de compaction | Reconstruir el filtro | N/A |

### Por qué no soporta Delete

Cada bit puede ser compartido por múltiples claves. Si borramos los bits de X, podemos desactivar bits que también pertenecen a Y:

```
Add("alice"): bits 3, 7, 12 → 1
Add("bob"):   bits 7, 9, 15 → 1

Delete("alice"): poner bits 3, 7, 12 → 0
MayContain("bob"): bit 7 == 0 → ¡falso negativo! BUG
```

Solución: Counting Bloom Filter (contadores en lugar de bits). No se implementa aquí; la reconstrucción en compaction es más simple.

### Conexión con sistemas reales

- **RocksDB**: Bloom Filter por cada SSTable. Antes de leer un SSTable del disco, verificar el filtro. Reduce disk I/O en 90%+ para workloads con many misses.
- **Cassandra**: Bloom Filters por partition key para evitar SSTables innecesarios.
- **Chrome**: usó Bloom Filters para la Safe Browsing list (detectar URLs maliciosas sin enviar todas las URLs a Google).
- **Bitcoin**: SPV wallets usan Bloom Filters para filtrar transacciones relevantes.

---

## 5. Sorted Slice + Binary Search

### El índice primario

El índice primario mapea `id → offset_en_WAL`. Con este índice, `Get(id)` no necesita leer el WAL completo: va directamente al offset correcto con `ReadAt`.

Opciones de estructura para el índice:

| Estructura | Lookup | Insert | Range | Memoria |
|---|---|---|---|---|
| Hash Map | O(1) amortizado | O(1) | No | O(N) |
| Sorted Slice | O(log N) | O(N) | Sí | O(N) |
| B-Tree | O(log N) | O(log N) | Sí | O(N) |
| Skip List | O(log N) prom | O(log N) prom | Sí | O(N log N) |

Se elige **Sorted Slice** por educativo: expone binary search explícitamente y contrasta con el hash map del índice secundario. La limitación (inserción O(N)) es documentada y aceptable para datasets educativos.

### Binary Search

```
Buscar id "d" en [a, b, c, d, e, f, g]:
  lo=0, hi=6, mid=3 → slice[3]="d" → encontrado en 1 paso

Buscar id "f":
  lo=0, hi=6, mid=3 → "d" < "f" → lo=4
  lo=4, hi=6, mid=5 → "f" == "f" → encontrado en 2 pasos
```

Para N=1,000,000 elementos: máximo 20 comparaciones (log₂(1M) ≈ 20).

### ReadAt: acceso aleatorio en archivos

`file.ReadAt(buf, offset)` llama a `pread(2)` en Linux/macOS: lee desde un offset específico sin mover la posición del archivo. Es **seguro bajo concurrencia**: múltiples goroutines pueden llamar `ReadAt` simultáneamente sobre el mismo `*os.File`.

```
Flujo de Get(id):
  1. Bloom Filter check → O(1)
  2. Binary search en sorted slice → O(log N) comparaciones de string
  3. file.ReadAt(offset) → 1 syscall, 0 seeks
  4. Deserializar JSON → O(payload_size)
```

### Trade-off vs. cargar todo en RAM

| Estrategia | RAM necesaria | Latencia de Get |
|---|---|---|
| Todo en RAM (Fase 1) | O(datos totales) | O(1) hash map |
| Índice en RAM + datos en disco | O(índice solamente) | O(log N) + 1 disk read |
| Todo en disco (sin índice) | O(1) | O(tamaño del WAL) full scan |

Para 1M docs de 1KB cada uno: 1GB de datos. Con el índice, solo necesitamos ~24 bytes × 1M = 24MB en RAM.

---

## 6. Min-Heap y TopK

### El problema: sort parcial

```
Find({ sortBy: "score", limit: 10 }, sobre 100,000 docs)
```

Opción A: sort total → O(M log M) = O(100,000 × 17) ≈ 1.7M comparaciones
Opción B: min-heap de tamaño K=10 → O(M log K) = O(100,000 × 3.3) ≈ 330K comparaciones

Para K=10, la opción B es ~5x más rápida. Para K=2, más de 10x.

### Cómo funciona el Min-Heap para TopK

El heap mantiene los K elementos máximos vistos hasta ahora. El mínimo del heap es el "umbral": solo documentos con valor > mínimo entran.

```
Invariante: heap[0] = mínimo de los K mejores vistos

Para cada documento doc:
  si len(heap) < K:
    insertar doc (heap crece)
  si doc.score > heap[0].score:  ← mínimo del heap
    reemplazar heap[0] con doc
    heapify-down para restaurar invariante
  si doc.score <= heap[0].score:
    ignorar doc (no puede estar en el top K)
```

**¿Por qué min-heap para los K máximos?**

Contra-intuitivo: queremos los K más grandes, pero usamos min-heap.
La razón: el mínimo es el candidato a ser expulsado. Necesitamos acceso rápido al mínimo para comparar cada nuevo elemento. Un max-heap no nos daría esto.

```
Max-heap: acceso O(1) al máximo → útil para extraer el top 1
Min-heap: acceso O(1) al mínimo → útil para mantener los top K (el mínimo es el "guardia")
```

### Propiedad del heap

```
Para un min-heap almacenado en array:
  hijo_izq(i) = 2i + 1
  hijo_der(i) = 2i + 2
  padre(i)    = (i-1) / 2

Invariante: array[i] ≤ array[2i+1] y array[i] ≤ array[2i+2]
```

### heapify-up y heapify-down

```
heapify-up (después de insertar al final):
  mientras nuevo > padre:
    swap(nuevo, padre)
    subir

heapify-down (después de reemplazar raíz):
  mientras raíz > algún hijo:
    swap(raíz, hijo_menor)
    bajar
```

### Conexiones con sistemas reales

- **Priority queues en sistemas operativos**: el scheduler de Linux usa un heap para seleccionar el proceso con mayor prioridad.
- **Dijkstra's algorithm**: usa un min-heap para seleccionar el nodo no visitado con menor distancia.
- **RocksDB compaction**: usa un min-heap para K-way merge de SSTables (se verá en Fase 9b).
- **Sistemas de eventos**: timers en Nginx, libuv, Go runtime usan heaps de tiempo.

---

## 7. BST Naïve

### Conceptos básicos

Un Binary Search Tree organiza elementos de modo que para cualquier nodo X:
- Todos los elementos del subárbol izquierdo < X
- Todos los elementos del subárbol derecho > X

```
         [30]
        /    \
     [20]    [50]
    /   \   /   \
  [10] [25][40] [70]
```

Lookup, insert, delete: O(altura del árbol). En un árbol perfectamente balanceado, altura = log₂(N).

### El problema del desbalanceo

Si los datos se insertan en orden ascendente:

```
Insert(10) → Insert(20) → Insert(30) → Insert(40) → Insert(50)

[10]
   \
   [20]
      \
      [30]
         \
         [40]
            \
            [50]
```

El árbol degenera en una **lista enlazada**: altura = N, todas las operaciones O(N).

**Este es el aprendizaje central de la Fase 5a**: demuestra por qué el balance es necesario. No basta con la estructura correcta; la forma del árbol importa.

### Test de desbalanceo

```go
bst := engine.NewBST()
for i := 0; i < 10_000; i++ {
    bst.Insert(float64(i), int64(i))
}
// Altura esperada: ~10,000 (lista enlazada)
// Altura de BST balanceado: ~14 (log₂(10,000))
```

### Range queries en BST

El in-order traversal de un BST visita los nodos en orden ascendente:

```go
func inorder(node, min, max, result):
    if node == nil: return
    if node.key > min:
        inorder(node.left, min, max, result)   // podría haber elementos válidos
    if min <= node.key <= max:
        result.append(node.offsets...)
    if node.key < max:
        inorder(node.right, min, max, result)  // podría haber elementos válidos
```

Con early exit (pruning), no se visitan subárboles fuera del rango. Esto es más eficiente que un scan lineal cuando el rango es pequeño relativo a N.

---

## 8. AVL Tree

### El invariante de balance

Un AVL Tree mantiene en cada nodo:

```
|altura(subárbol izquierdo) - altura(subárbol derecho)| ≤ 1
```

Esto garantiza que la altura total sea siempre ≤ 1.44 × log₂(N+2) − 1.44. Para N=10,000: máximo ~19 niveles (vs. 9,999 en BST desbalanceado).

### Balance Factor

```
BF(nodo) = altura(izq) - altura(der)

BF > 0: left-heavy
BF < 0: right-heavy
BF = 0: balanceado

Si |BF| > 1 → árbol necesita rebalanceo
```

### Las 4 rotaciones

Cuando se pierde el balance al insertar/eliminar, se aplica una de 4 rotaciones:

#### LL (Left-Left): hijo izquierdo y subinserción a la izquierda → right rotation

```
    Z (BF=+2)          Y
   / \               /   \
  Y   T4    →      X       Z
 / \              / \     / \
X   T3          T1  T2  T3  T4
```

#### RR (Right-Right): hijo derecho y subinserción a la derecha → left rotation

```
  X (BF=-2)              Y
 / \                   /   \
T1   Y        →      X       Z
    / \              / \     / \
   T2   Z          T1  T2  T3  T4
```

#### LR (Left-Right): hijo izquierdo, subinserción a la derecha → left rotation hijo, luego right rotation raíz

```
    Z (BF=+2)              Z              Y
   / \                   /   \          /   \
  X   T4   rotL(X)→    Y     T4 rotR(Z)→  X     Z
 / \                   / \              / \ / \
T1   Y               X   T3           T1 T2 T3 T4
    / \              / \
   T2  T3          T1   T2
```

#### RL (Right-Left): hijo derecho, subinserción a la izquierda → right rotation hijo, luego left rotation raíz

Similar a LR pero en espejo.

### Por qué existe el AVL entre BST y Skip List

Sin el AVL como paso intermedio, el salto de BST a Skip List parece arbitrario. El AVL hace explícito el costo de las rotaciones:

```
BST:       Sin rotaciones. Simple. Pero O(N) con datos ordenados.
AVL:       Con rotaciones. O(log N) garantizado. Pero 4 casos a manejar correctamente.
Skip List: Sin rotaciones. O(log N) probabilístico. Más simple que AVL.
```

La pregunta "¿por qué Skip List si ya tenemos AVL?" se responde sola después de implementar las 4 rotaciones.

### In-order successor para Delete

Cuando se elimina un nodo con dos hijos, se reemplaza con su **in-order successor** (el nodo más pequeño del subárbol derecho):

```
Delete [30] con dos hijos [20] y [50]:
  In-order successor = mínimo de subárbol derecho = [40]
  Copiar key/offsets de [40] a [30]
  Eliminar [40] del subárbol derecho (tiene ≤ 1 hijo)
  Rebalancear hacia arriba
```

### Complejidad

| Operación | BST (balanceado) | BST (worst case) | AVL |
|---|---|---|---|
| Lookup | O(log N) | O(N) | O(log N) |
| Insert | O(log N) | O(N) | O(log N) |
| Delete | O(log N) | O(N) | O(log N) |
| Range | O(k + log N) | O(k + N) | O(k + log N) |

Donde k = número de elementos en el rango.

---

## 9. Skip List

### La idea

Una Skip List es una lista enlazada **multinivel** donde los niveles superiores permiten "saltar" sobre muchos elementos:

```
Nivel 3: [−∞] ──────────────────────────→ [50] → [+∞]
Nivel 2: [−∞] ────────→ [20] ──────────→ [50] → [+∞]
Nivel 1: [−∞] → [10] → [20] → [30] ──→ [50] → [+∞]
Nivel 0: [−∞] → [10] → [15] → [20] → [30] → [40] → [50] → [+∞]
```

El nivel 0 contiene todos los elementos (lista enlazada completa). Los niveles superiores contienen una muestra aleatoria.

### Búsqueda

```
Buscar 30:
  Empezar en nivel más alto, en [−∞]
  Nivel 3: siguiente=[50] > 30 → bajar a nivel 2
  Nivel 2: siguiente=[20] ≤ 30 → avanzar a [20]
  Nivel 2: siguiente=[50] > 30 → bajar a nivel 1
  Nivel 1: siguiente=[30] == 30 → encontrado
```

La búsqueda hace log₂(N) saltos en promedio, igual que binary search.

### Inserción probabilística

Al insertar un nuevo elemento, se genera su nivel aleatoriamente:

```go
func randomLevel() int {
    level := 1
    for level < maxLevel && rand.Float64() < 0.5 {
        level++
    }
    return level
}
```

Con p=0.5:
- Nivel 1: 50% de los elementos
- Nivel 2: 25%
- Nivel 3: 12.5%
- Nivel k: (0.5)^k

No hay rotaciones. La aleatorización garantiza balance probabilístico.

### Comparación AVL vs. Skip List

| Aspecto | AVL Tree | Skip List |
|---|---|---|
| Worst case | O(log N) garantizado | O(N) teórico (raro) |
| Average case | O(log N) | O(log N) |
| Código | ~250 líneas (4 rotaciones) | ~150 líneas (sin rotaciones) |
| Concurrencia | Difícil (rotaciones modifican múltiples nodos) | Más fácil (lock-free posible) |
| In-order traversal | Recorrido inorder | Nivel 0 es una linked list |
| Memoria extra | 1 puntero extra por nodo | Array de punteros por nodo |
| Debugging | Verificar invariante de balance | Verificar que nivel 0 está ordenado |

### Por qué Skip List > AVL para este proyecto

1. **Lock-free**: en Redis, los sorted sets usan Skip Lists porque permiten implementación concurrente más simple. Las rotaciones del AVL modifican múltiples punteros, lo que complica el lock-free.

2. **Range scan trivial**: el nivel 0 de la Skip List ya es una linked list ordenada. Hacer range scan es `for node = node.next[0]; node.key <= max; node = node.next[0]`. En AVL, se necesita un iterador complejo.

3. **Código más simple**: menos casos de error, más fácil de depurar. En sistemas de producción, la simplicidad reduce bugs.

### Conexiones con sistemas reales

- **Redis Sorted Sets**: implementados con Skip Lists (para range queries) + hash maps (para acceso por key).
- **RocksDB MemTable**: Skip List como estructura principal antes del flush a SSTable.
- **LevelDB MemTable**: también Skip List.
- **Java ConcurrentSkipListMap**: implementación lock-free en la JVM.

---

## 10. Concurrencia: RWMutex y Atomics

### El problema de los data races

Un data race ocurre cuando dos goroutines acceden al mismo dato simultáneamente y al menos una escribe. El resultado es **undefined behavior**: el programa puede producir resultados incorrectos, crashear, o parecer correcto en el 99% de los casos.

```go
// DATA RACE: dos goroutines leen y escriben count sin sincronización
count = count + 1  // no es atómica: read → add → write (3 operaciones)
```

### sync.RWMutex: single-writer, multi-reader

```
mu.Lock()    → escritura exclusiva (bloquea a todos)
mu.RUnlock() → libera escritura

mu.RLock()   → lectura compartida (permite múltiples lectores, bloquea escritores)
mu.RUnlock() → libera lectura
```

**Por qué RWMutex y no Mutex**: las operaciones de lectura son la mayoría (Get, Find). Con Mutex, un Get bloquearía todos los demás Gets. Con RWMutex, N Gets corren en paralelo; solo los Inserts bloquean.

**Garantía de linearizabilidad**: cada operación parece ocurrir instantáneamente desde la perspectiva del llamador. No hay estado intermedio visible.

### pread(2): ReadAt es seguro sin lock

`file.ReadAt` usa la syscall `pread(2)`, que:
1. Lee desde un offset dado sin modificar la posición actual del file descriptor.
2. Es atómica a nivel del kernel para lecturas pequeñas.
3. Es segura para múltiples goroutines sobre el mismo `*os.File`.

Esto permite tener N goroutines haciendo `Get` simultáneamente, cada una con su propio `ReadAt`, sin lock adicional sobre el archivo.

### atomic.Int64 para IDs únicos

```go
id = fmt.Sprintf("%d-%d", time.Now().UnixNano(), counter.Add(1))
```

`atomic.Add` es una sola instrucción de hardware (`LOCK XADD` en x86). Dos goroutines que llaman al mismo nanosegundo obtienen contadores diferentes. IDs únicos sin lock.

### Go race detector

```bash
go test -race ./...
```

El race detector instrumenta el binario para trackear todos los accesos a memoria. Si dos goroutines acceden al mismo dato sin sincronización → panic con stack trace.

**Costo**: ~5x más lento en tiempo de ejecución, ~5x más RAM. Solo para testing, no para producción.

---

## 11. HyperLogLog

### El problema: contar distintos

```
¿Cuántas ciudades distintas tienen los 1,000,000 de documentos?
```

Respuesta exacta: hash set de todas las ciudades → O(N) memoria, O(N) tiempo.
Para N=1M strings de ciudad: ~50MB de RAM.

HyperLogLog responde con ~2% error usando **1 KB**.

### La intuición probabilística

Considera tirar una moneda hasta sacar cara. El número de tiros necesarios (k) sigue una distribución geométrica. Si el máximo k observado en M intentos es grande, intuitivamente había muchos intentos (muchos elementos distintos).

HyperLogLog usa bits de hash como "tiradas de moneda":
- Hashear el elemento → 64 bits de apariencia aleatoria.
- Contar los **leading zeros** en los bits (= número de "tiradas" hasta el primer 1).

```
hash("alice") = 0001 0110 ...
               ^^^^
               3 leading zeros → rank = 4 (leading zeros + 1)

Si el máximo rank observado es 4 → estimamos que hubo ~2^4 = 16 elementos distintos
```

### El estimador

Solo con el máximo rank, la estimación tiene mucha varianza. HyperLogLog la reduce dividiendo en M=1024 buckets:
- Usar los primeros log₂(M)=10 bits del hash para seleccionar un bucket.
- En cada bucket, guardar el máximo rank de los elementos que caen allí.
- Estimar usando la **media armónica** de 2^(-reg[i]):

```
E = α_m × m² × (Σ 2^(-reg[i]))^(-1)
```

La media armónica es más robusta que la aritmética: es menos sensible a outliers (un bucket con rank muy alto no domina la estimación).

### Correcciones de rango

```
Para N pequeño (< 2.5*m): LinearCounting  → más preciso para valores pequeños
Para N grande  (> 2^32/30): large-range correction → compensa colisiones de hash de 32 bits
```

### Por qué ~2% de error

El error estándar teórico es `1.04 / sqrt(m)`:
- m=16: error ≈ 26%
- m=64: error ≈ 13%
- m=1024: error ≈ 3.25%
- m=4096: error ≈ 1.6%

Con correcciones empíricas, el error real para m=1024 es ~2%.

### Trade-off exacto

| Método | Memoria para 1M distintos | Error |
|---|---|---|
| Hash Set exacto | ~50 MB | 0% |
| HyperLogLog m=1024 | 1 KB | ~2% |
| HyperLogLog m=16384 | 16 KB | ~0.8% |

Para un sistema de observabilidad, 2% de error es perfectamente aceptable.

### Merge: unión de conjuntos

```go
func (h *HyperLogLog) Merge(other *HyperLogLog) {
    for i := range h.regs {
        if other.regs[i] > h.regs[i] {
            h.regs[i] = other.regs[i]
        }
    }
}
```

El máximo elemento-a-elemento de los registros estima la cardinalidad de la **unión** de ambos conjuntos. Esto es propiedades muy valorada: se pueden hacer estimaciones distribuidas y luego mergear.

### Conexiones con sistemas reales

- **Redis HyperLogLog**: `PFADD`, `PFCOUNT`. 12 KB máximo, error < 1%.
- **Google BigQuery** y **Apache Spark**: HLL para `COUNT DISTINCT` aproximado en petabytes.
- **Facebook**: usa HLL para estimar audiencias únicas en ads.
- **Amazon Redshift**: HLL nativo para analytics.

---

## 12. Count-Min Sketch

### El problema: frecuencias en streams

```
¿Con qué frecuencia se consulta city='mx' en las últimas queries?
```

Respuesta exacta: hash map `{city_value → count}`. Para M valores distintos: O(M) memoria.

Count-Min Sketch responde con error acotado usando O(width × depth) memoria, independiente de M.

### Estructura

Una tabla 2D de contadores: `d` filas (depth) × `w` columnas (width). Cada fila usa una función hash diferente.

```
Add("mx"):
  row 0: h0("mx") % w = 42 → counters[0][42]++
  row 1: h1("mx") % w = 17 → counters[1][17]++
  row 2: h2("mx") % w = 73 → counters[2][73]++
  ...

Estimate("mx"):
  return min(counters[0][42], counters[1][17], counters[2][73], ...)
```

### Por qué el mínimo

Los contadores solo pueden ser mayores o iguales al verdadero count (porque otras claves pueden colisionar en el mismo slot). El mínimo es el mejor estimado:

```
True count("mx") = 100
Slot 42 (row 0): 100 (solo "mx" cayó aquí)
Slot 17 (row 1): 115 (también "paris" cayó aquí)
Slot 73 (row 2): 100 (solo "mx")

Estimate = min(100, 115, 100) = 100 ← exacto
```

### Garantía de error

```
P(estimate > true + ε × total_count) ≤ δ

donde:
  ε = e / width           (fracción de error sobre el total)
  δ = e^(-depth)          (probabilidad de exceder el error)

Para width=2000, depth=7:
  ε ≈ 0.00136  → error máximo 0.14% del total
  δ ≈ 0.0009   → probabilidad de exceder 0.09%
```

### CMS vs. HyperLogLog

| Aspecto | HyperLogLog | Count-Min Sketch |
|---|---|---|
| Pregunta que responde | ¿Cuántos distintos? | ¿Cuántas veces vi X? |
| Error | Relativo (~2%) | Absoluto (ε × N) |
| Puede subestimar | No | No |
| Puede sobreestimar | Sí | Sí |
| Soporta Delete | No | Parcialmente (counter negativo) |

Son **complementarios**: HLL dice "hay 50,000 ciudades distintas"; CMS dice "city='mx' aparece 12,000 veces".

### Conexiones con sistemas reales

- **Twitter Heavy Hitters**: CMS para detectar trending topics en tiempo real.
- **Apache Flink** y **Spark Streaming**: CMS para frecuencias en streaming sin estado unbounded.
- **AT&T Bell Labs** (el inventor, Cormode & Muthukrishnan, 2005): originalmente para análisis de tráfico de red.
- **Sistemas anti-DDoS**: detectar IPs con alta frecuencia de requests sin almacenar todas las IPs.

---

## 13. Compaction y Rename Atómico

### El problema del WAL append-only

El WAL solo crece. Después de 1M operaciones (muchas de ellas updates/deletes), el archivo puede contener 900,000 registros obsoletos y solo 100,000 documentos vivos.

```
Ejemplo WAL de 10 operaciones:
  INSERT alice (offset 0)
  INSERT bob   (offset 100)
  UPDATE alice (offset 200)   ← alice v1 en offset 0 es muerta
  INSERT carol (offset 300)
  DELETE bob   (offset 400)   ← bob en offset 100 es muerto
  UPDATE alice (offset 500)   ← alice v2 en offset 200 es muerta
  ...

Estado vivo: solo alice v3 (offset 500) y carol (offset 300)
Espacio desperdiciado: 80% del archivo
```

### El proceso de compaction

1. **Adquirir lock exclusivo**: detener toda actividad de escritura.
2. **Replay del WAL**: construir el estado vivo en memoria.
3. **Escribir WAL compacto**: un solo `INSERT` por documento vivo, en `data.log.compact`.
4. **`os.Rename`**: reemplazar `data.log` atómicamente.
5. **Reconstruir índices**: desde el WAL compacto.
6. **Reabrir WAL**: para nuevas escrituras.
7. **Liberar lock**.

### Rename atómico: la clave de la durabilidad

`os.Rename` en sistemas POSIX es **atómica**: el kernel garantiza que en cualquier momento el filename apunta a uno u otro archivo, nunca a un estado intermedio.

```
Escenario de crash durante rename:
  Si crash antes de rename: data.log.compact existe, data.log es el original → OK
  Si crash durante rename: kernel garantiza atomicidad → o viejo o nuevo
  Si crash después de rename: data.log es el nuevo, compact desapareció → OK

En startup: detectar data.log.compact huérfano → completar el rename
```

Esta garantía viene de la syscall `rename(2)` que manipula directamente los inodes del filesystem.

### Orphan detection

```go
func Open(path string) (*DB, error) {
    // Verificar si existe un compact huérfano del anterior crash
    if fileExists(path + ".compact") {
        os.Rename(path+".compact", path)  // completar la compaction interrumpida
    }
    // continuar con Open normal...
}
```

### Trade-off de compaction

| Aspecto | Compaction online | Compaction offline |
|---|---|---|
| Disponibilidad | DB parada durante compaction | — |
| Complejidad | Baja | — |
| Copy-on-write | DB sigue disponible | Complejo |
| Frecuencia | Bajo demanda / configurable | — |

Para un proyecto educativo, compaction offline (con lock) es la opción correcta. En producción (RocksDB, LevelDB), compaction es online y en background.

---

## 14. SSTables y K-way Merge

### Qué es un SSTable

Sorted String Table: un archivo **inmutable** con entradas ordenadas por clave. "Inmutable" es clave: una vez escrito, nunca se modifica. Para actualizar, se escribe un nuevo SSTable y se descarta el viejo en la siguiente compaction.

```
SSTable estructura:
┌─────────────────────────────────────┐
│ Data Block 1:                       │
│   ("alice", offset=0)               │
│   ("bob",   offset=100)             │
│   ...hasta K entradas               │
├─────────────────────────────────────┤
│ Data Block 2: ...                   │
├─────────────────────────────────────┤
│ Index Block:                        │
│   ("alice", block1_offset)          │  ← para binary search entre bloques
│   (primer_key_block2, block2_offset)│
├─────────────────────────────────────┤
│ Bloom Filter (serializado)          │  ← "¿está este ID aquí?"
├─────────────────────────────────────┤
│ Footer:                             │
│   index_offset, bloom_offset, magic │
└─────────────────────────────────────┘
```

### MemTable y el flujo de escritura

```
Insert/Update/Delete
       ↓
  WAL (durabilidad)
       ↓
  MemTable (Skip List en RAM)  ← escrituras rápidas
       ↓ (cuando MemTable supera K docs)
  flush a SSTable L0
       ↓ (cuando hay demasiados SSTables en L0)
  compaction → SSTable L1
       ↓ ...
  SSTable Ln
```

### Búsqueda con múltiples SSTables

```
Get(id):
  1. MemTable.Get(id) → encontrado → retornar
  2. Bloom(SSTable_L0).MayContain(id) → no → skip
  3. SSTable_L0.Get(id) → encontrado → retornar
  4. Bloom(SSTable_L1).MayContain(id) → no → skip
  5. ...
```

Los Bloom Filters evitan leer SSTables que no contienen el ID: en la práctica, la mayoría de los SSTables se saltan.

### K-way Merge con Min-Heap

Para combinar N SSTables en uno (compaction), hacer K-way merge:

```
Entradas iniciales del heap (una por SSTable):
  heap = [(alice, SST1), (carol, SST2), (bob, SST3)]

Paso 1: extraer mínimo = (alice, SST1)
  → escribir alice al SSTable de salida
  → leer siguiente de SST1 = (dave, SST1)
  → insertar en heap

Paso 2: heap = [(bob, SST3), (carol, SST2), (dave, SST1)]
  extraer = (bob, SST3) → escribir → leer siguiente de SST3...

Repetir hasta heap vacío
```

**Complejidad**: O(M log N) donde M = total de entradas, N = número de SSTables. El `log N` viene del heap de N elementos.

**Conexión con Fase 4**: el min-heap implementado para TopK de queries es exactamente el mismo algoritmo. Un mismo código, dos contextos completamente distintos: ordenar resultados de queries vs. compactar datos en disco.

### Por qué SSTables son mejores que un WAL puro para lecturas

| Aspecto | Solo WAL | WAL + SSTables |
|---|---|---|
| Write | O(1) append | O(1) append + occasional flush |
| Read | O(N) scan o O(log N) con índice en RAM | O(log N) en SSTable + Bloom |
| RAM necesaria | Todo el índice en RAM | Solo MemTable en RAM |
| Recovery | Replay completo del WAL | Solo el WAL desde el último checkpoint |

---

## 15. El LSM Tree — El Proyecto Completo

### Reconocimiento

Al llegar a Fase 9b, el proyecto implementa completamente un **Log-Structured Merge Tree (LSM Tree)**.

LSM Tree es la arquitectura detrás de:
- **RocksDB** (Meta, usada en MySQL, TiKV, CockroachDB, Kafka Streams)
- **LevelDB** (Google)
- **Cassandra** (Apache)
- **InfluxDB** (series de tiempo)
- **ScyllaDB** (Cassandra-compatible, C++)
- **BadgerDB** (Go, usada en Dgraph)

### Los 5 componentes del LSM Tree implementados

```
Componente         Fase    Archivo             Propósito
─────────────────────────────────────────────────────────────────
WAL                2       engine/wal.go       Durabilidad de writes
MemTable           5b      engine/skiplist.go  Escrituras rápidas en RAM
Bloom Filter       3       engine/bloom.go     Evitar disk reads innecesarios
Compaction         9       engine/compaction.go Reclamar espacio
SSTables + K-merge 9b      engine/sstable.go   Datos ordenados e inmutables en disco
```

### El flujo completo

```
Write path:
  client → WAL (fsync) → MemTable (Skip List) → retornar "OK"
  [background] MemTable llena → flush → SSTable L0

Read path:
  client → MemTable miss →
    Bloom(L0) → miss? skip L0 →
    Bloom(L1) → miss? skip L1 →
    SSTable L1 binary search → ReadAt → retornar doc

Compaction path (background):
  muchos SSTables en L0 → K-way merge con min-heap → SSTable L1
  muchos SSTables en L1 → K-way merge → SSTable L2
  ...
```

### La conexión min-heap ↔ compaction

El min-heap de Fase 4 (para TopK de queries) y el K-way merge de Fase 9b son exactamente el mismo algoritmo:

```
Fase 4 — TopK:
  heap mantiene los K documentos con mayor score vistos hasta ahora

Fase 9b — K-way merge:
  heap mantiene el menor elemento de cada SSTable (cursor)
  extrae el mínimo global → escribe al SSTable de salida
```

```go
// Fase 4 — el heap almacena documentos
heap.Push(doc)
top := heap.Pop()

// Fase 9b — el heap almacena cursores de SSTables
heap.Push(SSTablEntry{key, offset, sstable_id})
smallest := heap.Pop()
```

Un mismo algoritmo, dos niveles completamente diferentes del sistema.

### Por qué LSM Tree vs. B-Tree

Los dos árboles dominan el mundo de los motores de almacenamiento:

| Aspecto | LSM Tree | B-Tree |
|---|---|---|
| Write throughput | Alto (appends secuenciales) | Moderado (random writes in-place) |
| Read latency | Más alto (múltiples niveles) | Bajo (un árbol, lookup directo) |
| Write amplification | Media-alta (compaction reescribe datos) | Baja |
| Read amplification | Media (múltiples SSTables a consultar) | Baja |
| Space amplification | Media (datos muertos hasta compaction) | Baja |
| Casos de uso | Write-heavy (IoT, logs, analytics) | Read-heavy (OLTP, transaccional) |

PostgreSQL, MySQL/InnoDB: B-Tree.
RocksDB, Cassandra, InfluxDB: LSM Tree.

---

## 16. Ring Buffer — Replicación

### El problema

El leader procesa writes y necesita enviarlos al follower. Si el follower se desconecta temporalmente, ¿dónde guardamos los writes pendientes?

Opciones:
- **Cola ilimitada**: el leader nunca pierde datos pero la RAM crece sin límite.
- **Disco**: siempre disponible pero más lento; además, ya tenemos el WAL.
- **Ring Buffer (circular buffer)**: tamaño fijo, O(1) para push/pop, predecible en memoria.

### Estructura del Ring Buffer

```
Ring buffer de capacidad 8:

head →[4][5][6][7][ ][ ][ ][ ]← tail
       ^--- elementos disponibles ---^

head: índice del próximo a leer
tail: índice donde insertar el próximo

push(x): buffer[tail % cap] = x; tail++
pop():   x = buffer[head % cap]; head++; return x
len():   tail - head
full():  tail - head == cap
empty(): tail == head
```

Todo con operaciones de índice: sin malloc, sin punteros, cache-friendly.

### En replicación

El leader mantiene un ring buffer por follower:

```
Leader recibe writes → escribe en WAL propio → pone entry en ring buffer de cada follower

Follower conectado: consume del ring buffer → aplica entries → confirma
Follower desconectado: ring buffer acumula hasta cap entries
Follower reconecta: continúa desde donde quedó (si buffer no desbordó)
Buffer lleno → follower marcado como "lagged" → requiere full resync
```

### Conexiones con sistemas reales

- **Go channels**: son ring buffers internamente.
- **LMAX Disruptor**: ring buffer de ultra-baja latencia para sistemas financieros (millones de ops/sec).
- **Kafka**: cada partition es un WAL; los consumers tienen su propio offset (similar a head del ring buffer).
- **Linux kernel**: muchos subsistemas (networking, I/O) usan ring buffers para comunicación entre kernel y hardware.

---

## 17. Tabla de Trade-offs General

### Velocidad vs. Exactitud

| Estructura | Exacta | Probabilística | Trade-off |
|---|---|---|---|
| Hash Map | ✓ | — | RAM = O(N) |
| Sorted Slice | ✓ | — | Insert O(N) |
| BST | ✓ | — | Sin balance: O(N) worst |
| AVL | ✓ | — | Rotaciones complejas |
| Bloom Filter | — | ✓ | FP 0.8%; no Delete |
| HyperLogLog | — | ✓ | ~2% error; no Delete |
| Count-Min Sketch | — | ✓ | Sobreestima ε·N |
| Skip List | — | ✓ | O(log N) promedio, no garantizado |

### Memoria vs. Rendimiento

| Estrategia | RAM | Disk reads | Latencia |
|---|---|---|---|
| Todo en RAM (Fase 1) | O(datos) | 0 | O(1) |
| Índice en RAM (Fase 3) | O(índice) | 1 por Get | O(log N) |
| Bloom + Índice (Fase 3b) | O(índice + 10 bits/doc) | ~0 para miss | O(1) para miss |
| SSTable (Fase 9b) | O(MemTable) | O(niveles) para hit | O(log N × niveles) |

### Write vs. Read amplification

```
Write amplification: cuántas veces se escriben los datos por cada write del usuario
Read amplification: cuántas disk reads por cada read del usuario

Solo WAL:         WA=1, RA=1 (con índice en RAM)
WAL + compaction: WA=1 write + reescritura en compaction, RA=1
LSM Tree:         WA=alta (compaction multi-nivel), RA=Bloom reduce a ~1-2
B-Tree:           WA=baja (in-place), RA=baja (árbol balanceado)
```

### La jerarquía de complejidad implementada

```
Fase 1:  O(1) amortizado — Hash Map
Fase 3:  O(log N)        — Binary Search / Sorted Slice
Fase 4:  O(M log K)      — Heap TopK (mejor que O(M log M))
Fase 5a: O(N) worst      — BST naïve (degenera)
Fase 5a.5: O(log N)      — AVL (garantizado)
Fase 5b: O(log N) prom   — Skip List (probabilístico)
Fase 9b: O(M log N)      — K-way merge con heap

Estructuras probabilísticas:
Bloom:  O(1) con ~0.8% FP
HLL:    O(1) con ~2% error en cardinalidad
CMS:    O(1) con ε·N sobreestimación
```

---

*Este documento es una guía viva. Cada sección debe leerse junto con el código del archivo correspondiente en `engine/`. La teoría cobra sentido cuando se puede trazar directamente a una línea de código real.*
