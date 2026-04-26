# Phase 4 — Acceptance Test Manual

Motor de consultas + Min-Heap TopK.

Estos tests manuales verifican visualmente que todo lo implementado en la Fase 4 funciona correctamente: índice secundario, sort, TopK con heap, proyección y filtros combinados.

---

## Setup

```bash
# Desde la raíz del proyecto — poblar la BD con 50 documentos frescos
go run ./cmd/seed -count 50 -fresh

# Arrancar el REPL
go run ./cmd/repl.go
```

Cada documento tiene los campos: `_id`, `name`, `age`, `city`, `country`, `category`, `active`, `score`, `seq`.

---

## AT-01 — Índice secundario (strategy=secondary)

Un solo filtro `eq` → `ExecuteFind` usa `secondary.Lookup()` en lugar del scan completo.

```
> find city=mx
```

**Qué verificar:**
- El log imprime `strategy=secondary`.
- Todos los documentos devueltos tienen `"city": "mx"`.
- El conteo al final coincide con cuántos docs tienen `city=mx` en el seed.

---

## AT-02 — Scan completo sin filtro (strategy=full_scan)

Sin filtro → escanea el índice primario completo.

```
> find
```

**Qué verificar:**
- El log imprime `strategy=full_scan`.
- Se devuelven los 50 documentos.
- Los docs aparecen en orden del índice primario (ordenado por `_id` lexicográfico).

---

## AT-03 — Sort descendente sin límite (sortAll)

`sortAll` — insertion sort O(N log N), sin heap.

```
> find sort score desc
```

**Qué verificar:**
- El campo `score` decrece monotónicamente de un doc al siguiente.
- El log imprime `strategy=full_scan` (no hay filtro).
- Se devuelven los 50 documentos.

---

## AT-04 — Sort ascendente sin límite

```
> find sort age asc
```

**Qué verificar:**
- El campo `age` crece monotónicamente.
- El primero tiene el `age` más bajo, el último el más alto.

---

## AT-05 — TopK descendente (Min-Heap)

`topK` — min-heap de tamaño K, O(N log K). El heap procesa los 50 docs y conserva los 5 con mayor `score`.

```
> find sort score desc limit 5
```

**Qué verificar:**
- Se devuelven exactamente **5** documentos.
- El `score` del primero es el más alto de toda la colección.
- El orden es decreciente.
- Compara con `find sort score desc` (sin limit) — los 5 primeros deben ser idénticos.

---

## AT-06 — TopK ascendente (valores negados en el heap)

Para ASC, el heap negas los valores internamente para mantener la propiedad min-heap.

```
> find sort age asc limit 3
```

**Qué verificar:**
- Se devuelven exactamente **3** documentos.
- El `age` del primero es el más bajo de toda la colección.
- Compara con `find sort age asc` — los 3 primeros deben ser idénticos.

---

## AT-07 — Filtro + TopK (secondary index + heap)

Combina el índice secundario (candidatos reducidos) con el heap.

```
> find city=mx sort score desc limit 3
```

**Qué verificar:**
- El log muestra `strategy=secondary`.
- Se devuelven máximo 3 docs, todos con `"city": "mx"`.
- El `score` está en orden decreciente.
- Compara con `find city=mx sort score desc` — los 3 primeros deben coincidir.

---

## AT-08 — Limit sin sort (primeros K en orden de índice)

```
> find limit 10
```

**Qué verificar:**
- Se devuelven exactamente **10** documentos.
- El log imprime `strategy=full_scan`.
- No hay ningún orden particular de `score` o `age` (es el orden del índice primario).

---

## AT-09 — Limit mayor que el total (no trunca)

```
> find sort score desc limit 9999
```

**Qué verificar:**
- Se devuelven los 50 documentos (no explota con index out of range).
- El orden es decreciente por `score`.

---

## AT-10 — Find vacío (sin resultados)

```
> find city=atlantis
```

**Qué verificar:**
- El log imprime `strategy=secondary, candidates=0`.
- El REPL muestra `(no results)`.

---

## Verificación de logs esperados

Cada comando debe producir en la consola (nivel INFO) algo como:

```
[query] strategy  type=secondary  field=city  value=mx  candidates=12
[query] filter    strategy=secondary  candidates=12  matched=12
```

o para scan:

```
[query] strategy  type=full_scan  total_docs=50
[query] filter    strategy=full_scan  candidates=50  matched=50
```

Si ves `strategy=secondary` en un find sin filtro, o `strategy=full_scan` en un find con un solo `eq`, hay un bug en la selección de estrategia en `engine/query.go:ExecuteFind`.

---

## Archivos relevantes

| Archivo | Qué contiene |
|---|---|
| `engine/query.go` | `ExecuteFind`, selección de estrategia, `topK`, `sortAll`, `project` |
| `engine/heap.go` | `MinHeap`, `Push`, `Pop`, `siftUp`, `siftDown` |
| `engine/index.go` | `PrimaryIndex`, binary search, Bloom filter |
| `api/db.go` | `Find()` — coordina lock + dispatch a `ExecuteFind` |
| `tests/phase4_test.go` | Suite automática (cubre los mismos casos que este documento) |
