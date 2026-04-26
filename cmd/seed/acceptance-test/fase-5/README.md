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

## Archivos relevantes — Phase 5a

| Archivo | Qué contiene |
|---|---|
| `engine/bst.go` | `BST`, `RangeIndex`, `AddDoc`, `RemoveDoc`, `Query`, `Range` |
| `engine/query.go` | `ExecuteFind`, selección `range_bst`, `matchesAll` con ops de rango |
| `api/db.go` | `rangeIdx` en el struct DB; mantenimiento en Insert/Update/Delete |
| `tests/phase5a_test.go` | Suite automática (cubre los mismos casos que este documento) |

---

---

## Phase 5a.5 — AVL Tree como índice de rangos

El BST naïve de Phase 5a degenera en lista enlazada cuando los datos se insertan en orden: altura ≈ N, O(N) por búsqueda. El AVL Tree mantiene la invariante `|h(left) - h(right)| ≤ 1` mediante 4 casos de rotación (LL, RR, LR, RL), garantizando altura ≤ 1.44·log₂(N) en todo momento.

A partir de Phase 5a.5, el DB usa `RangeAVLIndex` (`engine/range_avl.go`) en lugar de `RangeIndex`. La interfaz pública es idéntica — el REPL no cambia.

---

## AT-09 — Rotaciones AVL visibles en los logs

```bash
go run ./cmd/seed -count 50 -fresh
```

**Qué verificar en los logs del seed:**
- Aparecen líneas con `[avl] rotation type=RR` (y/o `LL`, `LR`, `RL`).
- Esto confirma que el AVL está rebalanceando en cada insert.
- El BST naïve no emite estas líneas (no rota nunca).

El número de rotaciones depende del orden de inserción. Con IDs aleatorios el árbol crece casi balanceado — pocas rotaciones. Con claves ordenadas (ver AT-10) se disparan.

---

## AT-10 — Contraste de alturas BST vs AVL (test automático)

Este es el test educativo central de Phase 5a.5. Ejecuta:

```bash
go test ./tests/ -run TestAVLBalanceVsBST -v
```

**Output esperado:**

```
=== RUN   TestAVLBalanceVsBST
    phase5a5_test.go:XXX: N=200 sorted inserts
    phase5a5_test.go:XXX: BST height = 200  (expected ≈ 200, O(N) degenerate linked list)
    phase5a5_test.go:XXX: AVL height = 11   (expected ≤ 15,  O(log N) balanced — 1.44·log₂(200) ≈ 10.8)
--- PASS: TestAVLBalanceVsBST
```

**Qué significa:**
- BST: 200 inserts en orden ascendente → el árbol se convierte en una lista enlazada de profundidad 200. Cada búsqueda recorre todos los nodos: O(N).
- AVL: mismos 200 inserts → el árbol se rebalancea continuamente → profundidad ≈ 11. Cada búsqueda recorre 11 nodos: O(log N).
- Ambos devuelven **exactamente los mismos resultados** para `between [50, 100]` — el AVL es más rápido pero no cambia la semántica.

---

## AT-11 — REPL con AVL (mismos comandos que Phase 5a)

El REPL no cambia — el AVL opera de forma transparente.

```bash
go run ./cmd/seed -count 50 -fresh
go run ./cmd/repl.go
```

```
> find score>50
> find score>=80 sort score desc limit 5
> find age<30 sort age asc
```

**Qué verificar:**
- Los resultados son idénticos a Phase 5a.
- Los logs siguen mostrando `strategy=range_bst` (el nombre de la estrategia no cambia — el cambio es interno).
- Con `-v` en los tests se ven las alturas y las rotaciones.

---

## El arco completo: BST → AVL → Skip List

| Estructura | Complejidad | Invariante | Rotaciones |
|---|---|---|---|
| BST naïve (5a) | O(N) peor caso | Ninguna | 0 |
| AVL Tree (5a.5) | O(log N) garantizado | `|h(L)-h(R)| ≤ 1` | 4 casos |
| Skip List (5b) | O(log N) probabilístico | Niveles aleatorios | 0 |

La pregunta que responde Phase 5b: *¿podemos tener O(log N) garantizado* (probabilístico) *sin las 4 rotaciones del AVL?* La respuesta es la Skip List.

---

## Archivos relevantes — Phase 5a.5

| Archivo | Qué contiene |
|---|---|
| `engine/avl.go` | `AVLTree`, rotaciones LL/RR/LR/RL, `Range`, `GreaterThan`, `LessThan` |
| `engine/range_avl.go` | `RangeAVLIndex` — mismo interfaz que `RangeIndex` pero usa AVL |
| `engine/bst.go` | `RangeIndex.MaxTreeHeight` — para el test de contraste |
| `api/db.go` | `rangeIdx *engine.RangeAVLIndex` — swap del tipo (Phase 5a.5) |
| `tests/phase5a5_test.go` | Suite automática + `TestAVLBalanceVsBST` |