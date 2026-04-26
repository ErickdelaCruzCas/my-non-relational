# Fase 7 — Acceptance Tests: Migración JSON → MsgPack

El byte de versión por registro (posición 12 del header WAL v2) permite que
recovery despache al deserializador correcto sin conocer el formato global del
archivo. WALs mixtos (JSON + MsgPack en el mismo archivo) son manejados de
forma transparente.

---

## AT-01: Restart con MsgPack

```bash
go test ./tests/ -run TestMsgPackRestartSurvival -v
```

**Qué hace**: inserta un documento con `MsgPackSerializer`, cierra la DB,
la reabre y verifica que el documento tiene los valores correctos.

**Esperado**: `PASS`. Confirma que el byte de versión `0x02` en el header
es leído y despachado correctamente al deserializador MsgPack en recovery.

---

## AT-02: WAL mixto (JSON + MsgPack en el mismo archivo)

```bash
go test ./tests/ -run TestMixedWAL -v
```

**Qué hace**: abre la DB con JSON → inserta `id1` → cierra. Reabre con
MsgPack → inserta `id2` (mismo archivo WAL). Reabre de nuevo → verifica
que ambos documentos son accesibles.

**Esperado**: `PASS`. Simula una migración interrumpida. El recovery lee
el byte de versión de cada registro y elige el serializador adecuado.

---

## AT-03: Benchmark comparativo JSON vs MsgPack

```bash
go test ./tests/ -bench=. -benchmem -run=^$ -count=3
```

**Qué hace**: inserta N documentos con cada formato y mide ns/op y bytes/op.

**Esperado**: `BenchmarkMsgPack` es ~2-3× más rápido que `BenchmarkJSON`
y produce un WAL ~30-40% más pequeño. Los valores exactos dependen del
hardware; lo importante es la tendencia relativa.

---

## AT-04: Herramienta de migración v1 → v2

Requiere un WAL v1 previo (escrito antes de Fase 7). Para simular:

```bash
# 1. Crear un WAL v1 con la herramienta de seed
go run ./cmd/seed -dir /tmp/v1-data -count 10 -mode with-index

# 2. Migrar a v2 con formato MsgPack
go run ./cmd/migrate -src /tmp/v1-data/data.log -dst /tmp/v1-data/data.log.v2 -format msgpack

# Esperado:
# [migrate] source WAL: 10 live documents
# [migrate] output WAL:  10 documents written (format=msgpack)
# [migrate] done: /tmp/v1-data/data.log replaced atomically
```

**Verificación**: abrir la DB migrada y comprobar que los 10 documentos
son accesibles:

```bash
# En el REPL:
go run ./cmd/repl.go  # ajusta dbPath a /tmp/v1-data si es necesario
> find {}
```

---

## AT-05: Suite completa con race detector

```bash
go test ./tests/ -count=1 -race
```

**Esperado**: todos los tests pasan (incluyendo los nuevos de Fase 7),
cero data races detectadas.

---

## Garantías del formato WAL v2

| Campo   | Bytes | Cubierto por CRC | Descripción |
|---------|-------|------------------|-------------|
| size    | 0–3   | —                | `len(payload)` en LE uint32 |
| type    | 4–7   | —                | RecordInsert=1, Update=2, Delete=3 |
| crc32   | 8–11  | —                | CRC32 del payload únicamente |
| version | 12    | —                | FormatJSON=1, FormatMsgPack=2 |
| payload | 13…   | sí               | documento serializado |

El byte de versión **no** está cubierto por el CRC (está fuera del payload).
Si el byte de versión es desconocido, recovery usa `FormatJSON` como fallback
gracioso para compatibilidad con WALs v1 (donde el byte 12 era 0x00).
