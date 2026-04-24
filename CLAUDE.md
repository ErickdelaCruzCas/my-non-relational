# my-non-relational — Síntesis de proyecto

Base de datos de documentos JSON en Go, construida desde cero con fines educativos.
Detalle completo en README.md.

---

## Decisiones clave (no cambiar sin discutir)

- **Append-only log** como única forma de escribir. Sin overwrites.
- **CRC32 por registro WAL** (Fase 2): recovery rechaza registros con checksum inválido.
- **Documentos JSON** (`map[string]any` con `_id` reservado). Sin esquema fijo.
- **Serialización**: JSON primero (fases 1-6), migración a MsgPack en Fase 7.
- **Índice primario**: sorted slice de `(id, offset)` con binary search + Bloom Filter previo — no hash map.
- **Índice secundario**: hash map `field:value -> []offset`, persistido en `data/index.json`.
- **Sort con limit**: min-heap de tamaño K (TopK) en lugar de sort total cuando hay `limit`.
- **Índice de rangos**: BST naïve (5a) → AVL Tree (5a.5) → Skip List (5b). Contraste deliberado.
- **Single-writer + multi-reader** con `sync.RWMutex`. Sin MVCC.
- **Sin SQL, sin joins**. El motor de queries es limitado a propósito.
- **Sin dependencias externas** hasta Fase 7. Solo stdlib de Go.
- **Este proyecto es un LSM Tree** (nombrado en Fase 9b): WAL + MemTable + SSTables + Bloom + K-way Merge.

---

## Estructuras de datos implementadas desde cero

| Estructura | Fase | Uso real en el sistema |
|---|---|---|
| Hash Map (open addressing) | 1 | Almacenamiento principal en memoria |
| Robin Hood Hashing + backward shift | 1b | Upgrade: elimina tombstones, mejora lookup post-delete |
| CRC32 checksum | 2 | Integridad de cada registro WAL |
| Bloom Filter | 3 | Evita binary search + disk read para IDs inexistentes |
| Sorted Slice + Binary Search | 3 | Índice primario `id -> offset` |
| Min-Heap | 4 | TopK sort cuando `limit` está activo |
| BST naïve | 5a | Índice de rangos — primer intento, O(N) worst case |
| AVL Tree | 5a.5 | Índice de rangos — O(log N) garantizado, con rotaciones |
| Skip List | 5b | Índice de rangos — O(log N) probabilístico, sin rotaciones |
| Priority Queue (min-heap) | 8 | Slow query log: top-K queries más lentas |
| HyperLogLog | 8 | Cardinalidad estimada de campos indexados (~2% error) |
| Count-Min Sketch | 8 | Frecuencia de valores en queries (never underestimates) |
| SSTables + K-way Merge | 9b | LSM compaction; el mismo min-heap de Fase 4 reutilizado |
| Doubly Linked List + Hash Map | roadmap | LRU Cache de documentos calientes |

---

## Estructura de carpetas

```
engine/   hashmap.go  wal.go  storage.go  index.go  query.go  stats.go  explain.go
          recovery.go  bst.go  avl.go  skiplist.go  heap.go  compaction.go
          bloom.go  hll.go  cms.go  sstable.go
api/      db.go
cmd/      repl.go
data/     data.log  index.json
tests/    phaseN_test.go
```

---

## Fases

| # | Nombre | Estructura de datos nueva |
|---|---|---|
| 1 | Fundación + CRUD en memoria + REPL | Hash Map custom (open addressing) |
| 1b | Robin Hood Hashing | Robin Hood displacement + backward shift (misma interfaz) |
| 2 | Persistencia: WAL append-only + recovery | CRC32 por registro |
| 3 | Índice en disco + Binary Search | Sorted Slice + Binary Search + Bloom Filter |
| 4 | Motor de consultas + Min-Heap TopK | Min-Heap |
| 5a | BST naïve (rangos) | BST (O(N) worst case — educativo) |
| 5a.5 | AVL Tree (rangos) | AVL Tree (O(log N) garantizado, 4 rotaciones) |
| 5b | Skip List (rangos) | Skip List (O(log N) probabilístico, sin rotaciones) |
| 6 | Concurrencia formal | — |
| 7 | Migración JSON → MsgPack | — |
| 8 | Observabilidad + Priority Queue | Priority Queue + HyperLogLog + Count-Min Sketch |
| 9 | Compaction | Rename atómico POSIX |
| 9b | SSTables + K-way Merge (LSM Tree completo) | SSTables + K-way Merge (min-heap de Fase 4) |
| 10 | Replicación leader-follower (opcional) | Ring Buffer (WAL replication queue) |

---

## Fase actual

**Fase 2** — pendiente de inicio. Fase 1 completada (incluyendo Fase 1b: Robin Hood Hashing).
