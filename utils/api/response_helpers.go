package api

import "github.com/gofiber/fiber/v3"

// ResponseBuilder owns typed response construction and HTTP serialization.
// It is stateless and can be shared across requests.
type ResponseBuilder struct{}

var Responses ResponseBuilder

func (ResponseBuilder) NewResponse[T any](status int, message string, data *T) *GenericResponse[T] {
	return &GenericResponse[T]{
		Status:      status,
		Message:     message,
		UpdatedData: data,
	}
}

func (ResponseBuilder) UpdatedDataResponse[T any](c fiber.Ctx, status int, message string, data *T) error {
	return c.Status(status).JSON(Responses.NewResponse(status, message, data))
}

func (ResponseBuilder) ResponseWithStruct[T any](c fiber.Ctx, status int, data T) error {
	return c.Status(status).JSON(data)
}

func ErrorBadRequest(c fiber.Ctx, message string) error {
	return Responses.UpdatedDataResponse[string](c, fiber.StatusBadRequest, message, nil)
}

func ErrorUnauthorized(c fiber.Ctx, message string) error {
	return Responses.UpdatedDataResponse[string](c, fiber.StatusUnauthorized, message, nil)
}

func ErrorForbidden(c fiber.Ctx, message string) error {
	return Responses.UpdatedDataResponse[string](c, fiber.StatusForbidden, message, nil)
}

func ErrorNotFound(c fiber.Ctx, message string) error {
	return Responses.UpdatedDataResponse[string](c, fiber.StatusNotFound, message, nil)
}

func ErrorInternal(c fiber.Ctx, message string) error {
	return Responses.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, message, nil)
}

func (ResponseBuilder) SuccessResponse[T any](c fiber.Ctx, message string, data *T) error {
	return Responses.UpdatedDataResponse(c, fiber.StatusOK, message, data)
}
