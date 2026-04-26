// Package engine — Serializer abstraction
//
// # OCP + DIP: serialización desacoplada del motor
//
// db.go y recovery.go no importan encoding/json directamente. Dependen de
// esta interfaz, lo que permite sustituir JSON por MsgPack en Fase 7 con
// un único cambio de configuración en Open().
//
// Fase 7 añade MsgPackSerializer. El WAL v2 incluye un byte de versión por
// registro (FormatJSON=1 ó FormatMsgPack=2) que recovery usa para elegir el
// deserializador correcto sin conocer el formato global del archivo.
package engine

import (
	"encoding/json"

	msgpack "github.com/vmihailenco/msgpack/v5"
)

// Serializer encodes and decodes documents to/from bytes.
// Implementations must be safe for concurrent use.
type Serializer interface {
	// Marshal serializes v into bytes.
	Marshal(v any) ([]byte, error)

	// Unmarshal deserializes data into v.
	Unmarshal(data []byte, v any) error
}

// JSONSerializer implements Serializer using encoding/json.
// Produces human-readable output; default for Phases 1–6.
type JSONSerializer struct{}

func (JSONSerializer) Marshal(v any) ([]byte, error)      { return json.Marshal(v) }
func (JSONSerializer) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

// MsgPackSerializer implements Serializer using vmihailenco/msgpack/v5.
// ~3× faster than JSON on typical documents; ~40% smaller on disk.
// Introduced in Phase 7.
type MsgPackSerializer struct{}

func (MsgPackSerializer) Marshal(v any) ([]byte, error)      { return msgpack.Marshal(v) }
func (MsgPackSerializer) Unmarshal(data []byte, v any) error { return msgpack.Unmarshal(data, v) }
