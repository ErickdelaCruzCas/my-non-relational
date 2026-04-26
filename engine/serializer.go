// Package engine — Serializer abstraction
//
// # OCP + DIP: serialización desacoplada del motor
//
// db.go y recovery.go no importan encoding/json directamente. Dependen de
// esta interfaz, lo que permite sustituir JSON por MsgPack en Fase 7 con
// un único cambio de configuración en Open(), sin tocar el resto del código.
//
// En Fase 7 se añadirá MsgPackSerializer que implementa la misma interfaz.
// La firma de Open() recibirá un Config con el campo SerializationFormat.
package engine

import "encoding/json"

// Serializer encodes and decodes documents to/from bytes.
// Implementations must be safe for concurrent use.
type Serializer interface {
	// Marshal serializes v into bytes.
	Marshal(v any) ([]byte, error)

	// Unmarshal deserializes data into v.
	Unmarshal(data []byte, v any) error
}

// JSONSerializer implements Serializer using encoding/json.
// This is the default for Phases 1–6.
type JSONSerializer struct{}

func (JSONSerializer) Marshal(v any) ([]byte, error)      { return json.Marshal(v) }
func (JSONSerializer) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
