# my-non-relational — Guía de uso (Fase 1)

> Estado actual: **Fase 1 completada** — almacenamiento en memoria, sin persistencia en disco.
> Los datos se pierden al cerrar el proceso. La persistencia llega en Fase 2.

---

## Requisitos

- Go 1.22 o superior
- Sin dependencias externas

---

## Compilación y ejecución

```bash
# Desde la raíz del proyecto
go run ./cmd/repl.go

# O compilar y ejecutar el binario
go build -o dbserver ./cmd/repl.go
./dbserver
```

Al arrancar, el REPL imprime:

```
my-non-relational — Phase 1 (in-memory)
commands: insert <json>, get <id>, update <id> <json>, delete <id>, exit
>
```

---

## Ejecutar los tests

```bash
go test ./tests/ -run TestPhase1 -v
go test ./tests/ -run TestHashMap -v

# Con detección de data races
go test -race ./...
```

---

## Comandos del REPL

### `insert <json>`

Inserta un nuevo documento. El JSON debe ser un objeto (`{}`).

**Entrada:**
```
insert {"name": "alice", "age": 30, "city": "mx"}
```

**Salida:**
```
inserted: 1735000000000000000-1
```

El `_id` devuelto es generado automáticamente con el formato `<unixNano>-<counter>`.

---

#### Comportamiento del campo `_id`

| Situación | Comportamiento |
|---|---|
| No se incluye `_id` | Se genera automáticamente: `<unixNano>-<counter>` |
| Se incluye `"_id": "mi-id"` | Se usa ese valor como identificador |
| `_id` es string vacío `""` | Se ignora; se genera automáticamente |
| `_id` no es string (ej. número) | Se ignora; se genera automáticamente |
| `_id` ya existe en la base de datos | Error: `duplicate id` |

**Ejemplo con `_id` explícito:**
```
insert {"_id": "user-001", "name": "bob"}
```
```
inserted: user-001
```

**Ejemplo de duplicado:**
```
insert {"_id": "user-001", "name": "carol"}
```
```
error: duplicate id: user-001
```

---

### `get <id>`

Recupera un documento por su `_id`. Imprime el JSON con indentación.

**Entrada:**
```
get user-001
```

**Salida:**
```json
{
  "_id": "user-001",
  "name": "bob"
}
```

> El orden de los campos en la salida no está garantizado (comportamiento estándar de `map` en Go).

**ID no encontrado:**
```
get id-inexistente
```
```
error: not found: id-inexistente
```

**Sin argumento:**
```
get
```
```
usage: get <id>
```

---

### `update <id> <json>`

Actualiza un documento existente haciendo un **merge parcial** del JSON recibido sobre el documento almacenado.

- Los campos presentes en el JSON **sobreescriben** el valor existente.
- Los campos **ausentes** del JSON se **preservan** intactos.
- El campo `_id` **no puede cambiarse**.

**Estado inicial:**
```
insert {"_id": "u1", "name": "alice", "age": 30, "city": "mx"}
```

**Actualización parcial (solo cambia `age`):**
```
update u1 {"age": 31}
```
```
updated
```

**Verificación:**
```
get u1
```
```json
{
  "_id": "u1",
  "age": 31,
  "city": "mx",
  "name": "alice"
}
```

`name` y `city` permanecen intactos porque no estaban en el JSON de update.

---

#### Casos de error en `update`

**ID no encontrado:**
```
update id-inexistente {"age": 25}
```
```
error: not found: id-inexistente
```

**Intentar cambiar el `_id`:**
```
update u1 {"_id": "otro-id", "age": 32}
```
```
error: cannot change _id
```

**Pasar el mismo `_id` es válido** (no se considera un cambio):
```
update u1 {"_id": "u1", "age": 32}
```
```
updated
```

**JSON inválido:**
```
update u1 {age: 32}
```
```
error: invalid JSON: invalid character 'a' looking for beginning of object key string
```

**Sin argumentos suficientes:**
```
update u1
```
```
usage: update <id> <json>
```

---

### `delete <id>`

Elimina el documento con el `_id` dado.

**Entrada:**
```
delete user-001
```
**Salida:**
```
deleted
```

**ID no encontrado:**
```
delete id-inexistente
```
```
error: not found: id-inexistente
```

**Sin argumento:**
```
delete
```
```
usage: delete <id>
```

Un documento eliminado no puede recuperarse con `get` ni actualizarse con `update`.

---

### `exit`

Cierra el REPL limpiamente.

```
exit
```
```
bye
```

También se puede salir con `Ctrl+D` (EOF).

---

## Tipos de valores JSON soportados

El documento es un `map[string]any`. Se aceptan todos los tipos JSON válidos como valores:

| Tipo JSON | Ejemplo | Tipo en Go |
|---|---|---|
| string | `"alice"` | `string` |
| número entero o decimal | `30`, `3.14` | `float64` |
| booleano | `true`, `false` | `bool` |
| null | `null` | `nil` |
| array | `[1, 2, 3]` | `[]any` |
| objeto anidado | `{"x": 1}` | `map[string]any` |

> Los números JSON siempre se deserializan como `float64` en Go, independientemente de si son enteros o decimales.

---

## Comportamiento ante comandos desconocidos

```
foo
```
```
commands: insert <json>, get <id>, update <id> <json>, delete <id>, exit
```

---

## Limitaciones de la Fase 1

| Limitación | Descripción |
|---|---|
| Sin persistencia | Al cerrar el proceso, todos los documentos se pierden. Fase 2 introduce el WAL. |
| Sin búsqueda | No hay `find` ni filtros. Solo `get` por `_id` exacto. Fase 4 añade el motor de queries. |
| Sin índices secundarios | Solo se indexa `_id`. Fase 3 añade el índice primario en disco y el secundario. |
| Memoria como límite | El dataset completo vive en RAM. Fase 3 mueve los documentos al disco. |
| `_id` no validado | Cualquier string no vacío es un `_id` válido; no se verifica formato ni longitud. |
