package pagination

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestParsePositiveInt(t *testing.T) {
	t.Parallel()

	t.Run("default on empty", func(t *testing.T) {
		t.Parallel()
		got, err := ParsePositiveInt("", 10, "limit")
		if err != nil {
			t.Fatalf("ParsePositiveInt returned error: %v", err)
		}
		if got != 10 {
			t.Fatalf("ParsePositiveInt empty = %d, want 10", got)
		}
	})

	t.Run("valid int", func(t *testing.T) {
		t.Parallel()
		got, err := ParsePositiveInt("25", 10, "limit")
		if err != nil {
			t.Fatalf("ParsePositiveInt returned error: %v", err)
		}
		if got != 25 {
			t.Fatalf("ParsePositiveInt valid = %d, want 25", got)
		}
	})

	t.Run("invalid int", func(t *testing.T) {
		t.Parallel()
		_, err := ParsePositiveInt("abc", 10, "limit")
		if err == nil {
			t.Fatalf("ParsePositiveInt should fail for non-int")
		}
		fiberErr, ok := err.(*fiber.Error)
		if !ok {
			t.Fatalf("error type = %T, want *fiber.Error", err)
		}
		if fiberErr.Code != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", fiberErr.Code, fiber.StatusBadRequest)
		}
	})

	t.Run("non-positive", func(t *testing.T) {
		t.Parallel()
		_, err := ParsePositiveInt("0", 10, "limit")
		if err == nil {
			t.Fatalf("ParsePositiveInt should fail for zero")
		}
		fiberErr, ok := err.(*fiber.Error)
		if !ok {
			t.Fatalf("error type = %T, want *fiber.Error", err)
		}
		if fiberErr.Code != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", fiberErr.Code, fiber.StatusBadRequest)
		}
	})
}

func TestParsePageAndPageSize(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		page, pageSize, err := ParsePageAndPageSize(c, 1, 20, 100)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.SendStatus(fiberErr.Code)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}
		return c.JSON(map[string]int{
			"page":      page,
			"page_size": pageSize,
		})
	})

	t.Run("valid", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/?page=2&page_size=50", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}
	})

	t.Run("page_size too large", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/?page_size=101", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}

func TestTotalPagesAndHasMore(t *testing.T) {
	t.Parallel()

	tests := []struct {
		total      int
		pageSize   int
		wantPages  int
		page       int
		wantOffset bool
		wantPagesM bool
	}{
		{total: 0, pageSize: 20, wantPages: 0, page: 1, wantOffset: false, wantPagesM: false},
		{total: 101, pageSize: 20, wantPages: 6, page: 1, wantOffset: true, wantPagesM: true},
		{total: 100, pageSize: 20, wantPages: 5, page: 5, wantOffset: false, wantPagesM: false},
	}

	for i, tc := range tests {
		tc := tc
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()
			gotPages := CalculateTotalPages(tc.total, tc.pageSize)
			if gotPages != tc.wantPages {
				t.Fatalf("CalculateTotalPages(%d,%d)=%d, want %d", tc.total, tc.pageSize, gotPages, tc.wantPages)
			}
			gotOffset := HasMoreByOffset(tc.page, tc.pageSize, tc.total)
			if gotOffset != tc.wantOffset {
				t.Fatalf("HasMoreByOffset(%d,%d,%d)=%v, want %v", tc.page, tc.pageSize, tc.total, gotOffset, tc.wantOffset)
			}
			gotPagesMore := HasMoreByTotalPages(tc.page, tc.wantPages)
			if gotPagesMore != tc.wantPagesM {
				t.Fatalf("HasMoreByTotalPages(%d,%d)=%v, want %v", tc.page, tc.wantPages, gotPagesMore, tc.wantPagesM)
			}
		})
	}
}
