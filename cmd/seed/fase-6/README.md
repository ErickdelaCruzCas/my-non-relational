# Fase 6 — Acceptance Tests: Concurrencia formal

El modelo de concurrencia de esta fase garantiza que múltiples lectores y un
único escritor puedan operar sin data races. Los tests requieren el race
detector de Go (`-race`) para ser significativos.

---

## AT-01: Race detector limpio bajo carga mixta

```bash
go test ./tests/ -run TestConcurrentReadWrite -race -count=5
```

**Qué hace**: 100 goroutines lectoras + 10 escritoras durante 200ms contra la misma DB.

**Esperado**: `PASS` las 5 veces. Cero líneas con `DATA RACE` en la salida.

---

## AT-02: IDs únicos bajo 500 inserts concurrentes

```bash
go test ./tests/ -run TestUniqueIDsUnderConcurrency -race -count=3
```

**Qué hace**: 500 goroutines insertan simultáneamente. Verifica que todos los `_id` son distintos.

**Esperado**: `PASS`. Si el generador de IDs tuviera una race condition, aparecerían duplicados.

---

## AT-03: Contadores atómicos exactos

```bash
go test ./tests/ -run TestAtomicCounters -v
```

**Qué hace**: ejecuta 2 Insert, 2 Get, 1 Find, 1 Update, 1 Delete y verifica que `Stats()` devuelve exactamente `{Writes:3, Reads:3, Deletes:1}`.

**Esperado**: `PASS`. Los contadores son atómicos — `Stats()` no requiere `db.mu`.

---

## AT-04: Suite completa con race detector

```bash
go test ./tests/ -count=1 -race
```

**Esperado**: todos los tests pasan, cero data races detectadas.

---

## Garantías del modelo de concurrencia

| Operación | Mecanismo | Por qué |
|---|---|---|
| Insert / Update / Delete | `db.mu.Lock()` | Un único escritor a la vez |
| Get / Find | `db.mu.RLock()` | Múltiples lectores simultáneos |
| `ReadAt` en WAL | ninguno | `pread(2)` es concurrente-seguro por diseño POSIX |
| `Stats()` | ninguno | Solo `atomic.Load()`, sin estado mutable compartido |
| Generación de `_id` | `atomic.Add()` | Unicidad garantizada sin lock |
| Sin starvation de escritura | `sync.RWMutex` | Go impide que un escritor espere indefinidamente si hay lectores continuos |
