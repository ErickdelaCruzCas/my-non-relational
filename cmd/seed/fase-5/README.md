# Phase 5a — Acceptance Test Manual

BST Naïve + Consultas de Rango.

Estos tests manuales verifican que las consultas de rango funcionan correctamente: el BST selecciona los candidatos correctos, el log muestra la estrategia `range_bst`, y los resultados son coherentes con inserts, updates y deletes.

---

## Setup

```bash
# Desde la raíz del proyecto — poblar la BD con 50 documentos frescos
go run ./cmd/seed -count 50 -fresh

# Arrancar el REPL
go run ./cmd/repl.go
```

Campos disponibles en cada documento: `_id`, `name`, `age` (20–64), `city`, `country`, `category`, `active` (0/1), `score` (0–99), `seq`.

---

## AT-01 — Rango gt (mayor que)

```
> find score>50
```

**Qué verificar:**
- El log imprime `strategy=range_bst`.
- Todos los documentos devueltos tienen `"score" > 50`.
- Ningún documento tiene `score` igual a 50 (boundary excluido).

**Cómo cruzar:** Compara con `find sort score asc` — todos los docs con score > 50 en esa lista deben aparecer aquí.

---

## AT-02 — Rango gte (mayor o igual)

```
> find score>=50
```

**Qué verificar:**
- El log imprime `strategy=range_bst`.
- Todos los documentos devueltos tienen `"score" >= 50`.
- Los documentos con `score == 50` sí aparecen (boundary incluido).

---

## AT-03 — Rango lt (menor que)

```
> find age<30
```

**Qué verificar:**
- El log imprime `strategy=range_bst`.
- Todos los documentos devueltos tienen `"age" < 30`.
- Ningún documento tiene `age == 30`.

---

## AT-04 — Rango lte (menor o igual)

```
> find age<=30
```

**Qué verificar:**
- El log imprime `strategy=range_bst`.
- Todos los documentos devueltos tienen `"age" <= 30`.
- Los documentos con `age == 30` sí aparecen.

---

## AT-05 — Rango + sort + limit (BST + Min-Heap)

```
> find score>50 sort score desc limit 3
```

**Qué verificar:**
- El log imprime `strategy=range_bst`.
- Se devuelven exactamente **3** documentos.
- Los 3 tienen `score > 50` y están ordenados de mayor a menor.
- Compara con `find score>50 sort score desc` (sin limit) — los 3 primeros deben ser idénticos.

---

## AT-06 — Consistencia tras Update (doc cruza la frontera)

Inserta un documento conocido, actualízalo y verifica que los rangos reflejan el nuevo valor.

```
> insert {"_id": "test-update", "score": 20}
> find score<50
```
El doc `test-update` debe aparecer (score 20 < 50).

```
> update test-update {"score": 80}
> find score<50
```
El doc `test-update` ya **no** debe aparecer.

```
> find score>50
```
El doc `test-update` ahora **sí** debe aparecer (score 80 > 50).

**Qué verificar:** El BST refleja el nuevo valor tras el update. No hay "fantasmas" del valor anterior.

---

## AT-07 — Consistencia tras Delete

```
> insert {"_id": "test-delete", "score": 75}
> find score>50
```
El doc `test-delete` aparece.

```
> delete test-delete
> find score>50
```
El doc `test-delete` ya **no** debe aparecer.

**Qué verificar:** El BST elimina el offset correctamente al borrar un documento.

---

## AT-08 — Sin resultados (rango vacío)

```
> find score>999
```

**Qué verificar:**
- El REPL muestra `(no results)`.
- El log imprime `strategy=range_bst, candidates=0`.
- No hay error ni panic.

---

## Logs esperados por operación

| Comando | Log esperado |
|---|---|
| `find score>50` | `strategy=range_bst  field=score  op=gt  candidates=N` |
| `find city=mx` | `strategy=secondary  field=city  candidates=N` |
| `find` | `strategy=full_scan  total_docs=50` |

Si ves `strategy=full_scan` en un `find score>50`, hay un bug en la selección de estrategia en `engine/query.go:ExecuteFind`.

---

## Archivos relevantes

| Archivo | Qué contiene |
|---|---|
| `engine/bst.go` | `BST`, `RangeIndex`, `AddDoc`, `RemoveDoc`, `Query`, `Range` |
| `engine/query.go` | `ExecuteFind`, selección `range_bst`, `matchesAll` con ops de rango |
| `api/db.go` | `rangeIdx` en el struct DB; mantenimiento en Insert/Update/Delete |
| `tests/phase5a_test.go` | Suite automática (cubre los mismos casos que este documento) |