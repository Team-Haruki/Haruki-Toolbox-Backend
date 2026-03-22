package api

type GenericResponse[T any] struct {
	Status      int    `json:"status"`
	Message     string `json:"message"`
	UpdatedData *T     `json:"updatedData,omitempty"`
}
