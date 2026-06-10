package userprivateapi

import (
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"

	"github.com/gofiber/fiber/v3"
)

func mapPrivateGameAccountLookupError(err error) *fiber.Error {
	if err == nil {
		return nil
	}
	if postgresql.IsNotFound(err) {
		return fiber.NewError(fiber.StatusNotFound, "account binding not found")
	}
	return fiber.NewError(fiber.StatusInternalServerError, "failed to query game account")
}

func mapPrivateAuthorizationLookupError(err error) *fiber.Error {
	if err == nil {
		return nil
	}
	return fiber.NewError(fiber.StatusInternalServerError, "failed to verify authorization")
}

func mapPrivateDataQueryError(err error) *fiber.Error {
	if err == nil {
		return nil
	}
	return fiber.NewError(fiber.StatusInternalServerError, "failed to query user data")
}

func mapPrivateBindingOwnerError(binding *postgresql.GameAccountBinding) *fiber.Error {
	if binding == nil {
		return fiber.NewError(fiber.StatusNotFound, "account binding not found")
	}
	if binding.Edges.User == nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to query game account owner")
	}
	if binding.Edges.User.Banned {
		return fiber.NewError(fiber.StatusForbidden, "forbidden: account owner is banned")
	}
	return nil
}
