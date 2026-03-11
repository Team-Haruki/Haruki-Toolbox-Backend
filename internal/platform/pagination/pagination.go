package pagination

import (
	"math"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func ParsePositiveInt(raw string, defaultValue int, fieldName string) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultValue, nil
	}
	v, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, fiber.NewError(fiber.StatusBadRequest, fieldName+" must be an integer")
	}
	if v <= 0 {
		return 0, fiber.NewError(fiber.StatusBadRequest, fieldName+" must be greater than 0")
	}
	return v, nil
}

func ParsePageAndPageSize(c fiber.Ctx, defaultPage, defaultPageSize, maxPageSize int) (int, int, error) {
	page, err := ParsePositiveInt(c.Query("page"), defaultPage, "page")
	if err != nil {
		return 0, 0, err
	}
	pageSize, err := ParsePositiveInt(c.Query("page_size"), defaultPageSize, "page_size")
	if err != nil {
		return 0, 0, err
	}
	if pageSize > maxPageSize {
		return 0, 0, fiber.NewError(fiber.StatusBadRequest, "page_size exceeds max allowed size")
	}
	return page, pageSize, nil
}

func CalculateTotalPages(total, pageSize int) int {
	if total <= 0 || pageSize <= 0 {
		return 0
	}
	return int(math.Ceil(float64(total) / float64(pageSize)))
}

func HasMoreByOffset(page, pageSize, total int) bool {
	if total <= 0 || page <= 0 || pageSize <= 0 {
		return false
	}
	return page*pageSize < total
}

func HasMoreByTotalPages(page, totalPages int) bool {
	if page <= 0 || totalPages <= 0 {
		return false
	}
	return page < totalPages
}
