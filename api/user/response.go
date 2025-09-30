package user

import "github.com/gofiber/fiber/v2"

type GenericResponse[T any] struct {
	Status      int    `json:"status"`
	Message     string `json:"message"`
	UpdatedData *T     `json:"updatedData,omitempty"`
}

func NewResponse[T any](status int, message string, data *T) *GenericResponse[T] {
	return &GenericResponse[T]{
		Status:      status,
		Message:     message,
		UpdatedData: data,
	}
}

func UpdatedDataResponse[T any](c *fiber.Ctx, status int, message string, data *T) error {
	return c.Status(status).JSON(NewResponse(status, message, data))
}

func ResponseWithStruct[T any](c *fiber.Ctx, status int, data T) error {
	return c.Status(status).JSON(data)
}
