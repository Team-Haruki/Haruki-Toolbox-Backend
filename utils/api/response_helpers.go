package api

import "github.com/gofiber/fiber/v3"

func NewResponse[T any](status int, message string, data *T) *GenericResponse[T] {
	return &GenericResponse[T]{
		Status:      status,
		Message:     message,
		UpdatedData: data,
	}
}

func UpdatedDataResponse[T any](c fiber.Ctx, status int, message string, data *T) error {
	return c.Status(status).JSON(NewResponse(status, message, data))
}

func ResponseWithStruct[T any](c fiber.Ctx, status int, data T) error {
	return c.Status(status).JSON(data)
}

func ErrorBadRequest(c fiber.Ctx, message string) error {
	return UpdatedDataResponse[string](c, fiber.StatusBadRequest, message, nil)
}

func ErrorUnauthorized(c fiber.Ctx, message string) error {
	return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, message, nil)
}

func ErrorForbidden(c fiber.Ctx, message string) error {
	return UpdatedDataResponse[string](c, fiber.StatusForbidden, message, nil)
}

func ErrorNotFound(c fiber.Ctx, message string) error {
	return UpdatedDataResponse[string](c, fiber.StatusNotFound, message, nil)
}

func ErrorInternal(c fiber.Ctx, message string) error {
	return UpdatedDataResponse[string](c, fiber.StatusInternalServerError, message, nil)
}

func SuccessResponse[T any](c fiber.Ctx, message string, data *T) error {
	return UpdatedDataResponse(c, fiber.StatusOK, message, data)
}
