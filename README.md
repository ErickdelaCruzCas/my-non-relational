# my-non-relational — Diseño técnico por fases

Base de datos de documentos JSON en Go, construida desde cero con fines educativos.
Cada fase introduce una estructura de datos o concepto real usado en motores de bases de datos de producción.

---

## Principios del proyecto

- **Persistencia real**: todo escrito sobrevive a un reinicio.
- **Append-only log**: las escrituras nunca sobreescriben. Esto simplifica la durabilidad y el recovery.
- **Documentos JSON**: cada registro es `map[string]any`. Flexible, sin esquema fijo.
- **Sin SQL, sin joins**: el motor de queries es deliberadamente limitado.
- **Complejidad explícita**: cada trade-off está nombrado donde ocurre. No hay magia.
- **Sin dependencias externas** hasta Fase 7. Solo stdlib de Go.

---

## Modelo de documento

Cada documento es un `map[string]any`. El campo `_id` es string, obligatorio, generado automáticamente si no se provee.

**Formato de ID**: `{unixNano}-{atomicCounter}` — garantiza unicidad bajo concurrencia sin locks.

**Campos reservados**: `_id`. Cualquier otro campo es libre.

---

## Formato del WAL (Write-ahead logs) (Fases 2-6)

Archivo: `data/data.log`. Formato append-only, sin modificaciones in-place.

```
┌──────────┬──────────┬──────────┬──────────────────────────┐
│ 4 bytes  │ 4 bytes  │ 4 bytes  │ N bytes                  │
│  size    │  type    │  crc32   │  JSON payload             │
│ uint32LE │ uint32LE │ uint32LE │  {"_id":"...", ...}       │
└──────────┴──────────┴──────────┴──────────────────────────┘

Types: 1=INSERT, 2=UPDATE, 3=DELETE
```

El CRC32 (Fase 2) protege contra corrupción silenciosa en disco. Recovery rechaza
cualquier registro cuyo checksum no coincide con el payload.

Cada registro es autocontenido. El reader reconstruye el estado completo leyendo de inicio a fin.
En Fase 7 el formato cambia a MsgPack con un byte de versión en el header.

---

## Trade-offs principales

| Decisión | Lo que ganamos | Lo que sacrificamos |
|---|---|---|
| Append-only log | Durabilidad simple, recovery por replay | Espacio desperdiciado hasta compaction |
| CRC32 por registro WAL | Detección de corrupción silenciosa en disco | ~4 bytes extra por registro; costo de cómputo mínimo |
| Robin Hood Hashing | Sin tombstones; lookup estable post-delete masivo | Delete es O(k) en vez de O(1) para k slots shifted |
| Bloom Filter antes de binary search | 99.2% de lookups negativos evitan binary search | ~10 bits/elemento de RAM extra; 0.8% falsos positivos |
| Sorted slice + binary search (primario) | Lookups O(log N), soporte de rangos sobre IDs | Más lento que hash map O(1) amortizado |
| Hash map (secundario) | Lookups O(1) por valor exacto | No soporta rangos; colisiones bajo carga extrema |
| Min-heap para TopK | Sort parcial O(M log K) vs O(M log M) | Código más complejo que sort.Slice |
| BST naïve → AVL → Skip List | Contraste educativo completo (3 estructuras, 2 trade-offs) | AVL requiere rotaciones; Skip List usa más memoria que AVL |
| Single-writer RWMutex | Modelo simple sin conflictos | Throughput de escritura serializado |
| JSON (fases 1-6) | Legible, debuggeable, sin dependencias | Más lento y voluminoso que binary formats |
| MsgPack (fase 7+) | ~3x más rápido, ~40% menos espacio | Ilegible sin decoder; dependencia externa |
| HyperLogLog para cardinalidad | O(1 KB) para estimar cardinalidad de 1M elementos | ~2% error; no soporta delete |
| Count-Min Sketch para frecuencias | O(width×depth) fijo para frecuencias de stream | Puede sobreestimar; nunca subestima |
| SSTables + K-way merge | Lecturas desde índice ordenado en disco; compaction eficiente | Implementación más compleja; múltiples archivos |

---

## Arquitectura general

```
┌────────────────────────────────────────────┐
│              cmd/repl.go                   │  ← REPL interactivo
└───────────────────┬────────────────────────┘
                    │
┌───────────────────▼────────────────────────┐
│              api/db.go                     │  ← API pública
└──┬──────────┬──────────┬───────────────────┘
   │          │          │
┌──▼──┐  ┌───▼───┐  ┌───▼────────────────┐
│ WAL │  │ Index │  │ Query Engine       │
│+CRC │  │Manager│  │ (heap.go/query.go) │
└──┬──┘  └───┬───┘  └────────────────────┘
   │          │
┌──▼──────────▼──────────────────────────────┐
│           engine/storage.go                │  ← ReadAt por offset
└────────────────────────────────────────────┘
                    │
             [data/data.log]
             [data/index.json]
```

---

# FASES

---

## Fase 1 — Fundación + CRUD en memoria + REPL

### Objetivo
Definir el modelo de documento, la API pública y el REPL. Todo el estado vive en memoria; no hay disco todavía. Se implementa un **Hash Map custom** con open addressing para el almacenamiento de documentos.

### Estructura de datos: Hash Map (open addressing)
En lugar de usar el `map` built-in de Go, se implementa desde cero. Open addressing con linear probing: todos los pares (key, value) viven en un array contiguo; las colisiones se resuelven avanzando al siguiente slot.

**Por qué open addressing vs. chaining**: mejor localidad de caché (un solo array en memoria); se degrada predeciblemente con el load factor. Se usa load factor máximo de 0.7 antes de rehash.

**Trade-off vs. `map` de Go**: el built-in es más rápido en la práctica (usa técnicas de SIMD internamente). El custom hash map es más educativo — expone el mecanismo de colisión y rehash.

### API pública

```
Insert(doc map[string]any) → (id string, err error)
Get(id string) → (doc map[string]any, err error)
Update(id string, partial map[string]any) → error
Delete(id string) → error
Close() → error
```

`Insert` rechaza `_id` duplicado. `Update` hace merge de campos (no reemplaza el documento completo). `Delete` sobre ID inexistente retorna error.

### Archivos
- `api/db.go` — DB struct + métodos públicos + RWMutex
- `engine/hashmap.go` — HashMap custom con open addressing
- `cmd/repl.go` — REPL: comandos `insert`, `get`, `update`, `delete`, `exit`
- `tests/phase1_test.go`

### REPL
```
insert {"name":"alice","age":30}
get <id>
update <id> {"age":31}
delete <id>
exit
```

### Definition of Done
- `go build ./...` sin errores
- `go test ./tests/ -run TestPhase1` al 100%
- REPL: los 4 comandos funcionales en sesión manual
- `go vet ./...` sin warnings
- Hash Map propio pasa sus tests unitarios (insert, get, delete, colisión, rehash)

---

## Fase 1b — Robin Hood Hashing (upgrade del HashMap)

### Objetivo
Refactorizar `engine/hashmap.go` para eliminar los tombstones y reemplazarlos con Robin Hood displacement + backward shift deletion. La interfaz pública no cambia; todos los tests de Fase 1 deben pasar sin modificar.

### El problema de los tombstones
Con el HashMap de Fase 1, `Delete` deja una marca muerta (tombstone) en el slot. Esto es necesario para no romper las cadenas de probing: un elemento más adelante en la cadena dependía de que el slot "ocupado" no estuviera vacío.

El problema: los tombstones acumulados degradan la performance. Cada lookup debe seguir skipping tombstones hasta encontrar un slot vacío real. Si se insertan 50,000 docs y se borran 49,000, el HashMap queda lleno de tombstones y cada `Get` recorre casi toda la tabla.

### Robin Hood displacement
Al insertar, si el elemento actual lleva más probes que el elemento que ocupa el slot (DIB = distance from initial bucket), se intercambian. El "rico" (pocos probes) cede el slot al "pobre" (muchos probes). Resultado: varianza de DIBs minimizada en toda la tabla.

```
Insertar X con DIB=3, slot ocupado por Y con DIB=1:
  Y tiene menos probes que X → X toma el slot, Y continúa buscando
```

Esto permite la early exit en `Get`: si el DIB del slot actual es menor que nuestra probe distance, el elemento buscado no puede estar más adelante (habría desplazado al actual durante la inserción).

### Backward shift deletion
En vez de dejar un tombstone, después de borrar un elemento se desplazan los siguientes elementos hacia atrás hasta encontrar un slot vacío o un elemento en su slot ideal (DIB=0).

```
Antes: [A(0)] [B(1)] [C(2)] [_]
Borrar B:
  Vaciar slot de B
  C tiene DIB=2 > 0 → moverlo al slot de B, DIB=1
Después: [A(0)] [C(1)] [_] [_]
```

### Trade-off
- Tombstone delete: O(1). Lookup post-delete: degradado.
- Backward shift delete: O(k) donde k = largo del chain. Lookup: siempre O(1) amortizado.

Para el patrón de uso de esta DB (insert-heavy, delete moderado), backward shift es claramente mejor. Es la estrategia usada por Rust's `std::collections::HashMap`.

### Test observable
Insertar 5,000 docs, eliminar 4,500, hacer 100,000 lookups de IDs inexistentes. Con tombstones: cada lookup recorre casi toda la tabla. Con Robin Hood: early exit en 1-3 probes.

### Archivos
- `engine/hashmap.go` — refactor completo (misma interfaz pública)
- `tests/phase1_test.go` — todos los tests existentes deben pasar sin cambios

### Definition of Done
- Todos los tests de Fase 1 pasan sin modificar
- `go test -race ./...` sin data races
- No existe el tipo `entryState` ni la constante `stateTombstone` en el código

---

## Fase 1c — REPL amigable (colores, help, UX)

### Objetivo
Hacer el REPL cómodo de usar sin añadir dependencias externas (stdlib only hasta Fase 7). El usuario debe poder entender qué comandos hay disponibles y recibir feedback visual claro de cada operación.

### Funcionalidades

#### Comando `help`
Tabla clara con todos los comandos, sintaxis y ejemplo:
```
COMMANDS
  insert <json>           Inserta un documento.      e.g. insert {"name":"alice"}
  get <id>                Obtiene un documento.
  update <id> <json>      Fusiona campos en un documento existente.
  find                    Lista todos los IDs.
  find <campo>=<valor>    Lista IDs donde campo coincide con valor.
  delete <id>             Elimina un documento.
  help                    Muestra este mensaje.
  exit                    Salir.
```

#### Colores ANSI en stdout
Usando códigos ANSI directamente (sin dependencias):
- Operación OK → **verde** (`\033[32m`)
- Errores → **rojo** (`\033[31m`)
- IDs y resultados → **cyan** (`\033[36m`)
- Prompt `>` → **bold** (`\033[1m`)
- Detección automática de TTY: si stdout va a un pipe o archivo, los colores se desactivan solos.

**Trade-off**: autocompletar con Tab e historial de comandos requieren raw mode o dependencias externas. Se difieren a Fase 7 cuando se permitan deps (`liner` o `readline`). Documentado en `USAGE.md`.

### Archivos
- `cmd/repl.go` — colores ANSI, comando `help`, prompt mejorado, detección de TTY

### Definition of Done
- `help` imprime tabla completa y legible
- Éxito en verde, error en rojo, IDs en cyan
- Sin colores si `!isatty(stdout)` (pipe/redirect)
- `go test ./...` sin regresiones

---

## Fase 1d — `find`: listar y filtrar documentos

### Objetivo
Poder ver qué documentos hay en la base de datos sin conocer los IDs de antemano. Dos modos: listar todos los IDs o filtrar por campo.

### Dos modos

#### `find` — lista todos los IDs
```
> find
1745406000-1
1745406001-2
1745406002-3
(3 documentos)
```

#### `find <campo>=<valor>` — filtra por campo (full scan)
```
> find city=mx
1745406000-1
1745406002-3
(2 coincidencias)
```

Full scan del HashMap: iterar todos los buckets con `dib != -1`. Complejidad O(capacity) — aceptable en Fase 1. Será reemplazado por el índice secundario en Fase 3.

### Implementación

**`engine/hashmap.go`** — nuevo método `All() []map[string]any`:
iteración directa sobre los buckets vivos.

**`api/db.go`** — nuevo método `Find(filter map[string]string) []map[string]any`:
- Sin filtro → devuelve copia defensiva de todos los docs.
- Con filtro → compara `fmt.Sprintf("%v", doc[field]) == value`.

**`cmd/repl.go`** — comando `find` con parsing de `campo=valor`.

### Archivos
- `engine/hashmap.go` — `All() []map[string]any`
- `api/db.go` — `Find(filter map[string]string) ([]map[string]any, error)`
- `cmd/repl.go` — comando `find`, parsing de `campo=valor`
- `tests/phase1_test.go` — tests para `Find` sin filtro y con filtro

### Definition of Done
- `find` lista todos los IDs correctamente
- `find campo=valor` devuelve solo los docs que coinciden
- Docs eliminados no aparecen en `find`
- `go test -race ./...` sin data races

---

## Fase 2 — Persistencia: WAL append-only + recovery

### Objetivo
Cada operación `Insert/Update/Delete` escribe un registro en `data/data.log`. Al reiniciar, se hace replay del log de inicio a fin para reconstruir el estado en memoria. Los datos sobreviven a reinicios y crashes.

### CRC32 por registro
Cada registro incluye un checksum CRC32 del payload JSON. En recovery, si el checksum no coincide → el registro se descarta con un warning, no causa panic. Esto detecta:
- Corrupción silenciosa en disco (bit flip)
- Escrituras parciales al final del log (crash durante Append)

**Trade-off**: 4 bytes extra por registro + costo de cómputo mínimo (CRC32 es O(N) en el tamaño del payload). El beneficio: recovery nunca inserta un documento con datos incorrectos silenciosamente.

**Test observable**: truncar un byte del payload JSON, verificar que recovery descarta el registro en vez de parsear basura.

### Otras decisiones de diseño

**`file.Sync()` en cada escritura**: garantiza que el SO confirma la escritura en disco antes de retornar al cliente. Esto es ~100x más lento que no hacerlo, pero es la garantía de durabilidad más sencilla. El trade-off se expone explícitamente en el código y se medirá en Fase 8.

**Recovery**: replay completo del log, de inicio a fin. El último registro por `_id` gana. Un registro con CRC inválido al final del log se trata como fin normal, no como error fatal.

**Sin índice todavía**: el recovery carga todos los documentos vivos en el Hash Map de Fase 1. La memoria escala con el tamaño del dataset.

### API pública (nuevos contratos)

`Open(path)` ahora hace recovery antes de retornar. Imprime en startup:
```
[startup] recovery: N entries replayed, M docs restored, elapsed Xms
```

`Close()` hace Sync y cierra el archivo.

### Archivos
- `engine/wal.go` — WAL struct: Append(type, data) → (offset, err); CRC32 en cada registro
- `engine/recovery.go` — ReplayWAL(path) → (docs, result, err); valida CRC32 en replay
- `api/db.go` — Open actualizado para hacer recovery
- `tests/phase2_test.go`

### Definition of Done
- Datos sobreviven `Close()` + `Open()` (test de restart)
- WAL truncado al final no causa panic (test de tail corrupto)
- Registro con CRC inválido es rechazado en recovery (test con byte corrupto)
- Log de startup imprime stats de recovery
- `go test -race ./...` sin data races

---

## Fase 3 — Índice en disco + Binary Search + Bloom Filter

### Objetivo
En lugar de mantener todos los documentos en memoria, el índice primario guarda solo `id -> offset`. `Get` lee el documento desde disco por offset usando `ReadAt`. El índice se persiste en `data/index.json` y se recarga en startup; si falta o está corrupto, se reconstruye por replay del WAL.

### Estructura de datos: Sorted Slice + Binary Search

El índice primario es un slice de structs `{id string, offset int64}` mantenido **siempre ordenado por id**. Las búsquedas usan binary search: O(log N) en el número de documentos vivos.

**Por qué sorted slice en vez de hash map**:
1. Permite binary search como ejercicio explícito.
2. Soporta rangos sobre IDs (ej: todos los IDs entre "a" y "b").
3. Contrasta con el hash map del índice secundario — dos estructuras, dos trade-offs.
4. Prepara la intuición para el Skip List de Fase 5.

**Inserción ordenada**: `O(N)` en el peor caso (insertar al principio). Para un proyecto educativo con datasets de decenas de miles de docs, es aceptable. Se documenta la limitación.

**`file.ReadAt`**: a diferencia de `Read`, es seguro para acceso concurrente (usa `pread(2)` internamente). No requiere lock adicional para leer el archivo.

### Bloom Filter antes del binary search

Antes de cada binary search en `Get(id)`, consultar el Bloom Filter:

```
id → BloomFilter.MayContain(id)
       ├── false → return ErrNotFound  ← O(1), evita binary search + disk read
       └── true  → binary_search → ReadAt → doc
```

El Bloom Filter (en `engine/bloom.go`) usa 10 bits/elemento con 7 funciones hash. Tasa de falsos positivos: ~0.8%.

**Test observable**: 100,000 IDs inexistentes. Sin Bloom: 100,000 binary searches. Con Bloom: ~800 binary searches (el 0.8% de falsos positivos).

**Trade-off**: ~10 bits × N IDs extra RAM. Para 1M docs: ~1.2 MB. El Bloom Filter no soporta Delete: en Fase 9 (compaction), reconstruir desde cero.

### Índice secundario en disco
`data/index.json` tiene dos secciones:
- `primary`: lista ordenada de `[id, offset]`
- `secondary`: mapa `"field:value" → [offset, offset, ...]`

En startup: cargar `index.json`, verificar integridad spot-checking contra el WAL. Si hay inconsistencia → rebuild completo por replay.

### API pública

`Get(id)` ahora: Bloom check → binary search en sorted slice → `ReadAt(offset)` → deserializar.

`Insert/Update/Delete`: actualizar Bloom Filter, sorted slice, `index.json`, luego confirmar al cliente.

### Archivos
- `engine/bloom.go` — BloomFilter (ya implementado)
- `engine/index.go` — PrimaryIndex (sorted slice + binary search + Bloom Filter integrado) + SecondaryIndex
- `engine/storage.go` — ReadRecordAt(file, offset) → data
- `api/db.go` — Get/Insert/Update/Delete actualizados
- `tests/phase3_test.go`

### Definition of Done
- `Get` lee desde disco, no desde memoria de documentos
- Bloom Filter evita binary search para IDs inexistentes (medir binary searches evitados)
- Sorted slice mantiene orden tras inserciones y deletes
- Binary search retorna resultado correcto (test con 10,000 docs)
- `index.json` recargado correctamente en startup
- Rebuild correcto cuando `index.json` está ausente o corrupto
- `go test -race ./...` sin data races

---

## Fase 4 — Motor de consultas + Min-Heap TopK

> **Punto de extracción de `db.go`** — Al terminar esta fase, `db.go` acumula:
> CRUD, WAL writes, locking, generación de IDs, merge logic, y ahora el motor
> de queries. En un sistema real este sería el momento de extraer tipos separados:
> `QueryEngine`, `IndexManager`, `StorageManager`. En este proyecto continuamos
> con un único coordinador de forma deliberada — la acumulación progresiva es
> parte de la lección. Si en fases posteriores el archivo supera las 400 líneas,
> es una señal para iniciar la extracción.

### Objetivo
Implementar `Find(query)` con filtros de igualdad, sort, limit y projection. Cuando hay `limit` activo, usar un **min-heap** para TopK en lugar de sort total. Full scan del WAL como estrategia base (sin índice secundario todavía).

### Estructura de datos: Min-Heap

Un min-heap de tamaño K mantiene los K documentos con mayor valor del campo de sort. Al procesar cada documento del scan:
- Si el heap tiene menos de K elementos → insertar.
- Si el valor del doc es mayor que el mínimo del heap → reemplazar el mínimo.
- Si no → ignorar el doc.

**Complejidad**: O(M log K) donde M = docs totales, K = limit. Si M=100,000 y K=10, la diferencia vs. O(M log M) es drástica en práctica.

**Trade-off**: el heap solo tiene sentido cuando K << M. Para K ≈ M, sort total es más simple. La implementación detecta este caso y usa sort total como fallback.

**Min-Heap o Max-Heap**: depende del orden. Para `sort ASC`, se quiere el K más grande → min-heap (el mínimo es el candidato a reemplazar). Para `sort DESC`, se quiere el K más pequeño → max-heap.

### Pipeline de Find

```
FindRequest → [Plan] → [Scan] → [Filter] → [Heap/Sort] → [Limit] → [Projection] → []doc
```

En Fase 4, el plan siempre es `FULL_SCAN`. El índice secundario se agrega en Fase 5 (equality) y como `RANGE_SCAN` también en Fase 5.

### API pública

```
Find(req FindRequest) → ([]map[string]any, error)

FindRequest {
    Filters    []Filter          // campo + op + valor
    Limit      int               // 0 = sin límite
    SortBy     string            // campo de sort; "" = sin sort
    SortAsc    bool
    Projection []string          // nil = todos los campos; _id siempre incluido
}

Filter {
    Field    string
    Op       string  // "eq" en Fase 4; "gt","lt","gte","lte","between" en Fase 5
    Value    any
    ValueEnd any     // solo para "between"
}
```

### CRUD completo en esta fase

`Update(id, partial)`: merge de campos → escribe registro UPDATE en WAL → actualiza sorted index. Retorna error si `id` no existe.

`Delete(id)`: lee el doc para saber sus campos indexados → escribe registro DELETE → remueve del sorted index y del índice secundario.

### Archivos
- `engine/query.go` — FindRequest, Filter, pipeline de ejecución
- `engine/heap.go` — MinHeap / MaxHeap implementados desde cero
- `api/db.go` — Find, Update, Delete actualizados
- `tests/phase4_test.go`

### Definition of Done
- `Find` con filtro `eq`, sin índice → full scan correcto
- TopK con min-heap correcto (verificar vs. sort total con mismos datos)
- Sort ASC y DESC correcto
- Projection no incluye campos no pedidos (excepto `_id`)
- Docs eliminados no aparecen en Find
- Comparación de tipos JSON correcta (`float64(30) == 30`)
- `go test -race ./...` sin data races

---

## Fase 5 — BST → AVL → Skip List (índice de rangos)

Esta fase tiene tres sub-pasos deliberados. Cada uno reemplaza al anterior como índice activo; los anteriores se mantienen como referencia educativa.

### Fase 5a — BST naïve para rangos

Implementar un Binary Search Tree sin balanceo para el índice de rangos. Campos de rango se declaran en `Config.RangeFields []string`.

**Por qué empezar con BST naïve**: demuestra el problema del desbalanceo. Si los documentos se insertan con valores ordenados (score: 1, 2, 3, ...), el árbol degenera en una lista enlazada: búsqueda O(N) en lugar de O(log N). Esto se observa y mide con un test.

**Operaciones del BST**:
- `Insert(key float64, offset int64)`
- `Delete(key float64, offset int64)`
- `Range(min, max float64) → []int64` — recorrido inorder con early exit
- `GreaterThan(min float64) → []int64`
- `LessThan(max float64) → []int64`

**Test de desbalanceo**: insertar 10,000 valores en orden ascendente. Medir la profundidad del árbol. Debe ser ~10,000 (lista enlazada). Esto motiva el paso 5a.5.

### Fase 5a.5 — AVL Tree (O(log N) garantizado)

Implementar un AVL Tree: BST auto-balanceado que mantiene la invariante `|altura(izq) - altura(der)| ≤ 1` en cada nodo. Esto garantiza altura ≤ 1.44·log₂(N) siempre, sin importar el orden de inserción.

**Las 4 rotaciones**:
```
LL: nodo queda left-heavy con hijo left-heavy  → single right rotation
RR: nodo queda right-heavy con hijo right-heavy → single left rotation
LR: nodo left-heavy con hijo right-heavy        → rotateLeft(hijo) + rotateRight(nodo)
RL: nodo right-heavy con hijo left-heavy        → rotateRight(hijo) + rotateLeft(nodo)
```

**Test de balance**: insertar 10,000 valores en orden ascendente. Altura del AVL debe ser ≤ 20 (vs. 9,999 del BST). La misma API que el BST: `Insert`, `Delete`, `Range`, `GreaterThan`, `LessThan`.

**Por qué el AVL antes del Skip List**: sin el AVL, el estudiante no entiende *por qué* el Skip List es mejor. Con el AVL como referencia, el trade-off es concreto: "misma garantía O(log N) que AVL, pero sin el dolor de las rotaciones."

**Desventaja del AVL**: las rotaciones son correctas pero complicadas. Cualquier bug en las 4 rotaciones es difícil de depurar. El Skip List resuelve esto con aleatoriedad.

### Fase 5b — Skip List (reemplaza BST y AVL)

El Skip List es una lista enlazada multinivel con búsqueda probabilística O(log N) promedio. No requiere rotaciones. Parámetros: `p=0.5`, `maxLevel=16` (soporta hasta ~65,000 elementos con eficiencia).

```
Nivel 3: [-∞] ─────────────────────→ [40] → [+∞]
Nivel 2: [-∞] ──→ [10] ──────────→ [40] → [+∞]
Nivel 1: [-∞] → [10] → [25] → [30] → [40] → [+∞]
Nivel 0: [-∞] → [10] → [15] → [25] → [30] → [35] → [40] → [+∞]
```

Cada nodo: `{key float64, offsets []int64, next [maxLevel]*Node}`.

**Por qué Skip List > AVL para este caso**:
- Sin rotaciones → implementación más simple.
- O(log N) promedio garantizado probabilísticamente, sin casos degenerados.
- Recorrido en orden es trivial (nivel 0 es una lista enlazada ordenada).
- Usado en producción: Redis sorted sets, RocksDB memtable.

**Desventaja**: peor localidad de caché que B-Tree (nodos dispersos en heap). Documentada en código.

### El arco completo

```
BST naïve  → O(N) worst case con datos ordenados. 18 líneas. Un bug y está hecho.
AVL Tree   → O(log N) garantizado. Requiere 4 rotaciones. ~200 líneas.
Skip List  → O(log N) probabilístico. Sin rotaciones. ~100 líneas. Más simple que AVL.
```

### Nuevos filtros en Find

```
Op: "gt", "lt", "gte", "lte", "between"

Find con "between": {Field: "score", Op: "between", Value: 25.0, ValueEnd: 75.0}
```

### Estrategia de query engine

```
Filtro con campo en RangeFields → RANGE_SCAN (Skip List)
Filtro con campo en IndexedFields → INDEX_SCAN (hash map secundario)
Ninguno de los anteriores → FULL_SCAN
```

### Archivos
- `engine/bst.go` — BST naïve (Fase 5a; se mantiene como referencia educativa)
- `engine/avl.go` — AVL Tree (Fase 5a.5; ya implementado)
- `engine/skiplist.go` — Skip List (Fase 5b; reemplaza BST + AVL como índice activo)
- `engine/index.go` — RangeIndex sobre Skip List
- `engine/query.go` — nuevos operadores de filtro
- `tests/phase5_test.go`

### Definition of Done
- BST: test de desbalanceo con datos ordenados (medir profundidad)
- AVL: insertar 10K valores ordenados, verificar altura ≤ 20
- Skip List: Insert, Delete, Range, GreaterThan, LessThan correctos
- Skip List no se degrada con datos ordenados (test comparativo vs. BST y AVL)
- Find con `between`, `gt`, `lt` usa RANGE_SCAN
- Stale offsets filtrados (verificar con índice primario)
- Skip List reconstruido en recovery (replay del WAL)
- `go test -race ./...` sin data races

---

## Fase 6 — Concurrencia formal

### Objetivo
Formalizar el modelo de concurrencia. Garantizar ausencia de data races bajo carga mixta (100 lectores + 10 escritores simultáneos). Documentar garantías explícitas.

### Modelo de concurrencia

```
Write ops (Insert/Update/Delete): db.mu.Lock()
Read ops  (Get/Find/Explain):     db.mu.RLock()
ReadAt en archivo:                sin lock extra (pread es concurrente-seguro)
Stats/Health:                     contadores atómicos, sin lock
```

**Garantías documentadas en `api/db.go`**:
- Linearizabilidad por operación: cada Insert/Get/Find es atómico.
- Sin snapshot isolation: un Find puede ver el efecto de un Insert concurrente si el timing es exacto.
- Single-writer: dos Inserts concurrentes no se mezclan (uno bloquea al otro).
- Sin starvation de escritura: `sync.RWMutex` de Go previene que un writer espere indefinidamente si hay readers continuos.

### Generación de IDs

`fmt.Sprintf("%d-%d", time.Now().UnixNano(), atomicCounter.Add(1))`

El contador atómico garantiza unicidad incluso si dos goroutines llaman `Insert` en el mismo nanosegundo.

### Archivos
- `api/db.go` — RWMutex formalizado + comentario de garantías
- `engine/stats.go` — contadores atómicos: ReadsTotal, WritesTotal, DeletesTotal
- `tests/phase6_test.go` — tests con `-race`

### Definition of Done
- `go test -race ./...` sin data races (ejecutar al menos 5 veces)
- Test: 100 goroutines lectoras + 10 escritoras, 10 segundos, sin panic ni race
- Contadores atómicos de reads/writes correctos
- IDs únicos bajo concurrencia (test: 1,000 goroutines insertan simultáneamente, verificar sin duplicados)
- Garantías documentadas en comentario en `api/db.go`

---

## Fase 7 — Migración JSON → MsgPack

### Objetivo
Migrar el formato de serialización del WAL e índice de JSON a MsgPack. Añadir un byte de versión al header del WAL para distinguir formatos. Incluir una herramienta de migración y un benchmark comparativo.

### WAL v2

```
┌──────────┬──────────┬──────────┬──────────┬──────────────────────────┐
│ 4 bytes  │ 4 bytes  │ 4 bytes  │ 1 byte   │ N bytes                  │
│  size    │  type    │  crc32   │ version  │  payload                  │
│ uint32LE │ uint32LE │ uint32LE │ 1=JSON   │  JSON o MsgPack           │
│          │          │          │ 2=msgpk  │                           │
└──────────┴──────────┴──────────┴──────────┴──────────────────────────┘
```

Recovery detecta el version byte y usa el deserializador correcto. Un WAL mixto (migración parcial interrumpida) es válido y recuperable.

### Tool de migración

`cmd/migrate/main.go`: lee `data.log` en formato JSON, escribe `data.log.new` en MsgPack, verifica integridad (mismo número de documentos, mismo contenido), renombra atómicamente.

### Config

```
Config.SerializationFormat string  // "json" | "msgpack"
```

`index.json` también migrado a `index.msgpack` cuando el formato cambia.

### Benchmark

Medir y reportar:
- Writes/sec: JSON vs MsgPack (mismo hardware, mismo dataset)
- WAL size: JSON vs MsgPack (mismo dataset)
- Recovery time: JSON vs MsgPack

### Archivos
- `engine/wal.go` — soporte para WAL v2 con version byte
- `engine/recovery.go` — detección de version byte
- `cmd/migrate/main.go` — herramienta de migración
- `tests/phase7_test.go` — migración sin pérdida; benchmark

### Definition of Done
- WAL v2 con version byte funcional
- Recovery detecta y maneja ambos formatos (JSON y MsgPack)
- Tool de migración: datos intactos después de migrar (verificación doc-a-doc)
- Benchmark reporta mejora medible en velocidad y tamaño
- WAL mixto (interrumpido) es recuperable sin pérdida de datos

---

## Fase 8 — Observabilidad + Priority Queue + HyperLogLog + Count-Min Sketch

### Objetivo
Implementar `Stats`, `Health`, `Storage` y `Explain`. Histogramas de latencia p50/p95/p99 con buckets fijos y contadores atómicos. **Priority Queue (min-heap)** para el slow query log. **HyperLogLog** para cardinalidad de campos indexados. **Count-Min Sketch** para frecuencia de valores en queries.

### Estructura de datos: Priority Queue para Slow Query Log

Un min-heap de tamaño K (configurable, default K=10) mantiene las K queries más lentas observadas. Al finalizar cada `Find`:
- Si el heap tiene menos de K elementos → insertar.
- Si la latencia de esta query > mínimo del heap → reemplazar el mínimo.
- Si no → ignorar.

**Por qué min-heap para top-K máximos**: intuitivamente parecería que se necesita max-heap, pero para mantener los K más grandes, se usa min-heap — el mínimo es el elemento "más fácil de desalojar". Si el nuevo elemento es mayor que el mínimo, entra al set y el mínimo sale.

**Invariante**: el heap siempre contiene los K queries más lentos vistos hasta ahora.

### HyperLogLog para cardinalidad

`engine/hll.go` (ya implementado). Cada campo indexado tiene su propio `HyperLogLog`. Al insertar un doc, se hace `hll.Add(doc[field])` para cada campo indexado.

**Uso en Explain**: `EstimatedDocs` en `ExplainPlan` ya no es 0. Si el query filtra `city='mx'` y el HLL para `city` estima 50,000 valores distintos con una distribución uniforme sobre 1M docs, el estimado es `1M / 50K = 20 docs`. Esto informa la decisión `INDEX_SCAN` vs `FULL_SCAN`.

**Trade-off**: hash set exacto de 1M strings: ~50 MB. HyperLogLog: ~1 KB. Error: ~2%.

### Count-Min Sketch para frecuencias

`engine/cms.go` (ya implementado). Un CMS global registra la frecuencia de cada valor de campo en las queries recibidas. Ejemplo: "city='mx' fue consultado 12,000 veces".

**Uso en Explain**: `IndexHitRatio` puede desglosarse por valor. El query planner puede priorizar el índice para valores de alta frecuencia.

**Trade-off**: no subestima, puede sobreestimar ε·N. Para w=2000, d=7: ε=0.14%, δ=0.09%.

### Histogramas de latencia

Buckets en microsegundos: `[50, 100, 250, 500, 1000, 2500, 5000, 10000, +inf]`.
Cada bucket es un contador atómico. `Observe(d)` incrementa el bucket correspondiente en O(log B) con binary search sobre los bounds.
`Percentile(p)` busca el bucket donde la frecuencia acumulada supera `p * total`.

### API pública

```
Stats() → StatsResult {
    ReadsTotal, WritesTotal, DeletesTotal int64
    GetP50, GetP95, GetP99               time.Duration
    PutP50, PutP95, PutP99               time.Duration
    FindP50, FindP95, FindP99            time.Duration
    IndexHitRatio                        float64
    IndexHits, IndexMisses               int64
    PrimaryIndexSize                     int
    WALSizeBytes                         int64
    LiveDocuments                        int64
    UptimeSeconds                        int64
    // HLL-powered:
    FieldCardinalities                   map[string]uint64  // campo → cardinalidad estimada
}

Health() → HealthResult {
    Status       string   // "ok" | "degraded" | "error"
    WALWritable  bool
    IndexLoaded  bool
    Issues       []string
}

Storage() → StorageInfo {
    WALPath       string
    WALSizeBytes  int64
    WALSizeHuman  string  // "12.3 MB"
    LiveDocs      int64
    DeadEntries   int64
    WasteRatio    float64
}

Explain(req ExplainRequest) → ExplainPlan {
    Strategy      string  // "FULL_SCAN" | "INDEX_SCAN" | "RANGE_SCAN" | "PRIMARY_LOOKUP"
    IndexUsed     string
    EstimatedDocs int     // powered by HyperLogLog
    // Si DryRun=false:
    DocsScanned   int
    DocsMatched   int
    Phases        []PhaseTime  // {Name, Duration, Detail}
    TotalTime     time.Duration
}
```

`Stats()` no toma el lock de la DB. Usa contadores atómicos independientes.

### Slow query log

Configurable via `Config.SlowQueryThreshold time.Duration` (default 100ms) y `Config.SlowQueryTopK int` (default 10).

Al superar el threshold: log estructurado + insertar en el priority queue de top-K.

```
[slow_query] elapsed=342ms strategy=FULL_SCAN filters=[{city eq mx}] docs_scanned=50000 matched=12
```

### Archivos
- `engine/stats.go` — histogramas, contadores atómicos, slow query priority queue, integración HLL y CMS
- `engine/explain.go` — ExplainPlan, EstimatedDocs via HLL, IndexHitRatio via CMS
- `engine/hll.go` — HyperLogLog (ya implementado)
- `engine/cms.go` — Count-Min Sketch (ya implementado)
- `api/db.go` — Stats, Health, Storage, Explain
- `tests/phase8_test.go`

### Definition of Done
- `Stats()` retorna valores correctos tras N operaciones
- Histogramas: p50/p95/p99 calculados correctamente (test con latencias conocidas)
- Priority Queue: los K más lentos son exactamente los correctos (test comparativo)
- Slow query log: queries lentas aparecen en el log + en el top-K
- `Explain(DryRun=true)` no toca el WAL
- `Explain(DryRun=false)` retorna tiempos reales por fase
- HLL: insertar 1M docs con 100 ciudades distintas, estimar cardinalidad — error < 5%
- CMS: frecuencias de query por campo correctamente registradas
- `Stats()` no requiere lock de la DB

---

## Fase 9 — Compaction

### Objetivo
Reclamar espacio en disco eliminando entradas muertas (updates y deletes anteriores). El WAL nuevo contiene solo el estado vivo. El reemplazo del archivo es atómico vía `os.Rename`.

### Proceso

1. Adquirir `db.mu.Lock()` — detener toda actividad.
2. Replay completo del WAL actual → construir estado vivo.
3. Escribir `data/data.log.compact` con solo los documentos vivos (un INSERT por doc).
4. `os.Rename("data/data.log.compact", "data/data.log")` — atómico en POSIX.
5. Reconstruir todos los índices (primary sorted slice, secondary hash maps, range skip lists) desde el nuevo WAL.
6. Reabrir el WAL para nuevas escrituras.
7. Liberar lock.

**`os.Rename` es atómico**: si el proceso muere durante el rename, el estado es o el WAL viejo o el nuevo. Nunca corrupto.

**Orphan detection**: si en startup existe `data/data.log.compact`, significa que el proceso murió entre escribir el compact y hacer el rename. Acción: completar el rename y reconstruir índices.

### API pública

```
Compact() → (CompactionResult, error)

CompactionResult {
    OldEntries      int
    NewEntries      int
    EntriesRemoved  int
    SpaceReclaimedBytes int64
    Duration        time.Duration
}
```

### Archivos
- `engine/compaction.go` — lógica de compaction + orphan detection
- `api/db.go` — Compact() + orphan check en Open()
- `tests/phase9_test.go`

### Definition of Done
- `Compact()` reduce el tamaño del WAL cuando hay entradas muertas
- Post-compaction: Get, Find, Find-with-range devuelven resultados correctos
- Restart post-compaction: DB arranca correctamente desde WAL compacto
- Orphan `.compact` detectado y resuelto en startup
- Métricas de compaction en `Stats()`
- Test: compact con mezcla de inserts, updates, deletes → solo estado vivo permanece

---

## Fase 9b — SSTables + K-way Merge (completar el LSM Tree)

### El momento "ajá": este proyecto ya es un LSM Tree

**Log-Structured Merge Tree** es la arquitectura detrás de RocksDB, LevelDB, Cassandra e InfluxDB. Sus componentes son exactamente los que ya implementamos:

```
WAL (append-only)           → Fase 2  ✓
MemTable (Skip List en RAM) → Fase 5b ✓
Compaction                  → Fase 9  ✓
Bloom Filters por nivel     → Fase 3  ✓
SSTables                    → Fase 9b ← esta fase
K-way Merge en compaction   → Fase 9b ← esta fase
```

La conexión más importante: el **min-heap de Fase 4**, implementado para TopK de queries, es exactamente el mismo algoritmo que RocksDB y LevelDB usan para hacer K-way merge de SSTables durante compaction. Un mismo algoritmo, dos contextos completamente distintos.

### Qué es un SSTable

Sorted String Table: un archivo binario **inmutable** con entradas ordenadas por clave. Permite:
- Binary search en O(log N) sin tener el índice en RAM.
- Range scans eficientes (lectura secuencial del archivo).
- Compaction mediante merge de múltiples SSTables (K-way merge).

### Estructura de un SSTable

```
┌─────────────────────────────────────────────────┐
│  Data Block 1: [(key1, offset1), ..., (keyK, offsetK)] │
│  Data Block 2: [(key1, offset1), ..., (keyK, offsetK)] │
│  ...                                             │
│  Index Block:  [(firstKey_block1, pos1), ...]    │  ← binary search entry
│  Footer: {indexOffset, indexSize, bloomOffset}   │
└─────────────────────────────────────────────────┘
```

### Flush: MemTable → SSTable

Cuando el sorted slice supera K docs (ej: 10,000), hacer flush a disco:
1. Escribir las entradas ordenadas como SSTable en `data/sstable_N.sst`.
2. Cada SSTable tiene su propio Bloom Filter (hereda de Fase 3).
3. El MemTable en RAM se vacía; las queries buscan: MemTable → SSTable L0 → SSTable L1.

### K-way Merge en compaction

Para fusionar N SSTables en uno solo, usar el min-heap de Fase 4:
1. Leer el primer elemento de cada SSTable → insertar en min-heap (N elementos).
2. Extraer el mínimo → escribir al SSTable de salida.
3. Insertar el siguiente elemento del SSTable del que vino el mínimo.
4. Repetir hasta que el heap esté vacío.

Complejidad: O(M log N) donde M = total de entradas, N = número de SSTables. El log N viene del min-heap.

### Bloom Filters por nivel

Cada SSTable tiene su propio Bloom Filter serializado en el footer. Antes de buscar en un SSTable, consultar su Bloom Filter: si dice "no existe", saltar el SSTable completamente.

### Archivos
- `engine/sstable.go` — SSTableWriter, SSTableReader, K-way merge
- `engine/compaction.go` — integrar flush + K-way merge
- `tests/phase9b_test.go`

### Definition of Done
- Flush: memtable → SSTable en disco, reiniciar DB, verificar datos intactos
- K-way merge: fusionar 3 SSTables con datos solapados, verificar resultado ordenado sin duplicados
- Bloom Filter por SSTable: lookup de ID inexistente no accede al SSTable
- Query pipeline: MemTable miss → SSTable L0 hit
- Compaction usa K-way merge (min-heap) para fusionar SSTables

---

## Fase 10 — Replicación leader-follower (opcional)

### Objetivo
Un proceso leader acepta writes. Uno o más followers reciben WAL entries via TCP y hacen replay. Los followers solo aceptan reads. Sin garantías de consistencia fuerte (eventual consistency).

### Estructura de datos: Ring Buffer

El leader mantiene un **ring buffer** de WAL entries pendientes de enviar a cada follower. Si el follower se desconecta, el buffer absorbe los entries hasta que reconecta o hasta que el buffer se llena (en cuyo caso el follower debe hacer full sync).

**Tamaño del ring buffer**: configurable, default 1000 entries. Si se llena → el follower se marca como "lagged" y debe hacer resync completo.

### Protocolo

- Leader envía entries TCP: `[4B size][WAL entry bytes]` — mismo formato que el WAL en disco.
- Follower hace replay de cada entry recibida usando la misma lógica de recovery.
- Follower rechaza writes con error: `"writes not accepted: follower mode"`.

### Config

```
Config.Role          string  // "leader" | "follower" | "standalone"
Config.FollowerAddrs []string  // solo en leader
Config.LeaderAddr    string    // solo en follower
```

### Archivos
- `engine/replication.go` — ring buffer, TCP sender (leader), TCP receiver (follower)
- `api/db.go` — modo follower: rechazar writes
- `tests/phase10_test.go`

### Definition of Done
- Write en leader es visible en follower tras propagación (test con delay configurable)
- Follower rechaza writes
- Reconexión del follower: continúa desde el último entry recibido (si el buffer no desbordó)
- Ring buffer: cuando se llena, follower marcado como lagged
- `go test -race ./...` sin data races

---

## Algoritmos y estructuras por fase — resumen

| Estructura / Algoritmo | Fase | Archivo |
|---|---|---|
| Hash Map (open addressing) | 1 | `engine/hashmap.go` |
| Robin Hood Hashing + backward shift | 1b | `engine/hashmap.go` |
| REPL con colores ANSI + help | 1c | `cmd/repl.go` |
| Full scan con filtro por campo | 1d | `engine/hashmap.go`, `api/db.go` |
| Append-only log writer + CRC32 | 2 | `engine/wal.go` |
| WAL replay con validación CRC32 | 2 | `engine/recovery.go` |
| Sorted Slice + Binary Search | 3 | `engine/index.go` |
| Bloom Filter (antes de binary search) | 3 | `engine/bloom.go` |
| `ReadAt` para acceso aleatorio | 3 | `engine/storage.go` |
| Full scan + early exit con limit | 4 | `engine/query.go` |
| Min-Heap (TopK sort) | 4 | `engine/heap.go` |
| Projection de campos | 4 | `engine/query.go` |
| BST naïve (índice de rangos) | 5a | `engine/bst.go` |
| AVL Tree (O(log N) garantizado) | 5a.5 | `engine/avl.go` |
| Skip List (índice de rangos) | 5b | `engine/skiplist.go` |
| `sync.RWMutex` single-writer | 6 | `api/db.go` |
| Contadores atómicos | 6, 8 | `engine/stats.go` |
| Histogramas con buckets fijos | 8 | `engine/stats.go` |
| Priority Queue (slow query log) | 8 | `engine/stats.go` |
| HyperLogLog (cardinalidad de campos) | 8 | `engine/hll.go` |
| Count-Min Sketch (frecuencia de queries) | 8 | `engine/cms.go` |
| Explain plan instrumentado | 8 | `engine/explain.go` |
| Rename atómico (POSIX) | 9 | `engine/compaction.go` |
| SSTables + K-way Merge | 9b | `engine/sstable.go` |
| Ring Buffer (replicación) | 10 | `engine/replication.go` |

---

*Este documento es la fuente de verdad del diseño. Cada fase se construye sobre la anterior. No saltar fases sin completar su Definition of Done.*
