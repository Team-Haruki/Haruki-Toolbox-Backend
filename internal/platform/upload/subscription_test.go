package upload

import (
	"slices"
	"testing"
)

func TestFilterBirthdayPartyPayloadKeepsMatchedDropsAndSamePositionFixtures(t *testing.T) {
	data := map[string]any{
		"upload_time": int64(1770000000),
		"server":      "jp",
		"updatedResources": map[string]any{
			"userMysekaiHarvestMaps": []any{
				map[string]any{
					"mysekaiSiteId": 5,
					"userMysekaiSiteHarvestResourceDrops": []any{
						map[string]any{"resourceType": "mysekai_material", "resourceId": 12, "positionX": 1.0, "positionZ": 2.0},
						map[string]any{"resourceType": "mysekai_material", "resourceId": 5, "positionX": 3.0, "positionZ": 4.0},
						map[string]any{"resourceType": "mysekai_material", "resourceId": 1, "positionX": 5.0, "positionZ": 6.0},
					},
					"userMysekaiSiteHarvestFixtures": []any{
						map[string]any{"mysekaiSiteHarvestFixtureId": 1001, "positionX": 1.0, "positionZ": 2.0},
						map[string]any{"mysekaiSiteHarvestFixtureId": 1002, "positionX": 5.0, "positionZ": 6.0},
					},
				},
			},
		},
	}

	filtered, matched, empty := FilterBirthdayPartyPayload(data, []int{12})
	if empty {
		t.Fatalf("expected non-empty result")
	}
	if len(matched) != 1 || matched[0] != 12 {
		t.Fatalf("matched ids = %+v, want [12]", matched)
	}

	updated := filtered["updatedResources"].(map[string]any)
	maps := updated["userMysekaiHarvestMaps"].([]any)
	if len(maps) != 1 {
		t.Fatalf("expected 1 map, got %d", len(maps))
	}
	site := maps[0].(map[string]any)
	drops := site["userMysekaiSiteHarvestResourceDrops"].([]any)
	if len(drops) != 1 {
		t.Fatalf("expected 1 drop, got %d", len(drops))
	}
	fixtures := site["userMysekaiSiteHarvestFixtures"].([]any)
	if len(fixtures) != 1 {
		t.Fatalf("expected 1 fixture, got %d", len(fixtures))
	}
}

func TestFilterBirthdayPartyPayloadDropsSameFixtureIDPoints(t *testing.T) {
	data := map[string]any{
		"updatedResources": map[string]any{
			"userMysekaiHarvestMaps": []any{
				map[string]any{
					"mysekaiSiteId": 5,
					"userMysekaiSiteHarvestResourceDrops": []any{
						map[string]any{"resourceType": "mysekai_material", "resourceId": 12, "positionX": 1.0, "positionZ": 2.0},
						map[string]any{"resourceType": "mysekai_material", "resourceId": 6, "positionX": 1.0, "positionZ": 2.0},
						map[string]any{"resourceType": "mysekai_material", "resourceId": 1, "positionX": 7.0, "positionZ": 8.0},
						map[string]any{"resourceType": "mysekai_item", "resourceId": 501, "positionX": 7.0, "positionZ": 8.0},
						map[string]any{"resourceType": "mysekai_material", "resourceId": 1, "positionX": 9.0, "positionZ": 10.0},
					},
					"userMysekaiSiteHarvestFixtures": []any{
						map[string]any{"mysekaiSiteHarvestFixtureId": 1001, "positionX": 1.0, "positionZ": 2.0},
						map[string]any{"mysekaiSiteHarvestFixtureId": 1001, "positionX": 7.0, "positionZ": 8.0},
						map[string]any{"mysekaiSiteHarvestFixtureId": 1002, "positionX": 9.0, "positionZ": 10.0},
					},
				},
			},
		},
	}

	filtered, _, empty := FilterBirthdayPartyPayload(data, []int{12})
	if empty {
		t.Fatalf("expected non-empty result")
	}
	site := filteredBirthdaySite(t, filtered)
	fixtures := site["userMysekaiSiteHarvestFixtures"].([]any)
	if len(fixtures) != 1 || fixtures[0].(map[string]any)["positionX"] != 1.0 {
		t.Fatalf("expected only the matched point, got %+v", fixtures)
	}
	drops := site["userMysekaiSiteHarvestResourceDrops"].([]any)
	if len(drops) != 1 || drops[0].(map[string]any)["resourceId"] != 12 {
		t.Fatalf("expected only the subscribed drop, got %+v", drops)
	}
}

func TestFilterBirthdayPartyPayloadDropsSameBirthdayPlants(t *testing.T) {
	data := map[string]any{
		"updatedResources": map[string]any{
			"userMysekaiHarvestMaps": []any{
				map[string]any{
					"mysekaiSiteId": 5,
					"userMysekaiSiteHarvestResourceDrops": []any{
						map[string]any{"resourceType": "mysekai_material", "resourceId": 17, "positionX": 1.0, "positionZ": 2.0},
						map[string]any{"resourceType": "mysekai_material", "resourceId": 297, "positionX": 1.0, "positionZ": 2.0},
						map[string]any{"resourceType": "mysekai_material", "resourceId": 297, "positionX": 7.0, "positionZ": 8.0},
					},
					"userMysekaiSiteHarvestFixtures": []any{
						map[string]any{"mysekaiFixtureId": 8027, "positionX": 1.0, "positionZ": 2.0},
						map[string]any{"mysekaiFixtureId": 8027, "positionX": 7.0, "positionZ": 8.0},
					},
				},
			},
		},
	}

	filtered, matched, empty := FilterBirthdayPartyPayload(data, []int{17, 11})
	if empty || len(matched) != 1 || matched[0] != 17 {
		t.Fatalf("matched = %+v empty = %v", matched, empty)
	}
	site := filteredBirthdaySite(t, filtered)
	if fixtures := site["userMysekaiSiteHarvestFixtures"].([]any); len(fixtures) != 1 {
		t.Fatalf("expected only the plant holding the battery, got %+v", fixtures)
	}
	if drops := site["userMysekaiSiteHarvestResourceDrops"].([]any); len(drops) != 1 || drops[0].(map[string]any)["resourceId"] != 17 {
		t.Fatalf("expected only the battery drop, got %+v", drops)
	}
}

func filteredBirthdaySite(t *testing.T, filtered map[string]any) map[string]any {
	t.Helper()
	maps := filtered["updatedResources"].(map[string]any)["userMysekaiHarvestMaps"].([]any)
	if len(maps) != 1 {
		t.Fatalf("expected 1 map, got %d", len(maps))
	}
	return maps[0].(map[string]any)
}

func TestFilterBirthdayPartyPayloadKeepsZeroPositionFixtures(t *testing.T) {
	data := map[string]any{
		"updatedResources": map[string]any{
			"userMysekaiHarvestMaps": []any{
				map[string]any{
					"mysekaiSiteId": 5,
					"userMysekaiSiteHarvestResourceDrops": []any{
						map[string]any{"resourceType": "mysekai_material", "resourceId": 12, "positionX": 0, "positionZ": 0},
					},
					"userMysekaiSiteHarvestFixtures": []any{
						map[string]any{"mysekaiSiteHarvestFixtureId": 1001, "positionX": 0, "positionZ": 0},
					},
				},
			},
		},
	}

	filtered, _, empty := FilterBirthdayPartyPayload(data, []int{12})
	if empty {
		t.Fatalf("expected non-empty result")
	}
	updated := filtered["updatedResources"].(map[string]any)
	maps := updated["userMysekaiHarvestMaps"].([]any)
	site := maps[0].(map[string]any)
	fixtures := site["userMysekaiSiteHarvestFixtures"].([]any)
	if len(fixtures) != 1 {
		t.Fatalf("expected zero-position fixture to be kept, got %d", len(fixtures))
	}
}

func TestFilterBirthdayPartyPayloadReportsEmptyResult(t *testing.T) {
	data := map[string]any{
		"updatedResources": map[string]any{
			"userMysekaiHarvestMaps": []any{
				map[string]any{
					"mysekaiSiteId": 5,
					"userMysekaiSiteHarvestResourceDrops": []any{
						map[string]any{"resourceType": "mysekai_material", "resourceId": 1, "positionX": 1.0, "positionZ": 2.0},
					},
					"userMysekaiSiteHarvestFixtures": []any{},
				},
			},
		},
	}

	filtered, matched, empty := FilterBirthdayPartyPayload(data, []int{12})
	if !empty {
		t.Fatalf("expected empty result")
	}
	if len(matched) != 0 {
		t.Fatalf("expected no matched ids, got %+v", matched)
	}
	updated := filtered["updatedResources"].(map[string]any)
	if maps := updated["userMysekaiHarvestMaps"].([]any); len(maps) != 0 {
		t.Fatalf("expected filtered maps empty, got %+v", maps)
	}
}

func TestFilterBirthdayPartyPayloadHandlesMsgpackNumericTypes(t *testing.T) {
	data := map[string]any{
		"updatedResources": map[string]any{
			"userMysekaiHarvestMaps": []any{
				map[any]any{
					"mysekaiSiteId": uint16(17),
					"userMysekaiSiteHarvestResourceDrops": []any{
						map[any]any{
							"resourceType": []byte("mysekai_material"),
							"resourceId":   uint8(12),
							"positionX":    int8(-2),
							"positionZ":    int16(-27),
						},
					},
					"userMysekaiSiteHarvestFixtures": []any{
						map[any]any{"mysekaiSiteHarvestFixtureId": uint16(1001), "positionX": int8(-2), "positionZ": int16(-27)},
					},
				},
			},
		},
	}

	filtered, matched, empty := FilterBirthdayPartyPayload(data, []int{12, 5, 20})
	if empty {
		t.Fatalf("expected msgpack numeric drop to match")
	}
	if len(matched) != 1 || matched[0] != 12 {
		t.Fatalf("matched ids = %+v, want [12]", matched)
	}
	updated := filtered["updatedResources"].(map[string]any)
	maps := updated["userMysekaiHarvestMaps"].([]any)
	if len(maps) != 1 {
		t.Fatalf("expected 1 map, got %d", len(maps))
	}
	site := maps[0].(map[string]any)
	drops := site["userMysekaiSiteHarvestResourceDrops"].([]any)
	if len(drops) != 1 {
		t.Fatalf("expected 1 drop, got %d", len(drops))
	}
	fixtures := site["userMysekaiSiteHarvestFixtures"].([]any)
	if len(fixtures) != 1 {
		t.Fatalf("expected 1 fixture, got %d", len(fixtures))
	}
}

func TestBirthdayBatteryAndAmethystFiltering(t *testing.T) {
	ids := materialIDsFromNames([]string{"battery", " amethyst ", "mysekai_material_17", "quartz", "mysekai_material_11"})
	if !slices.Equal(ids, []int{11, 17}) {
		t.Fatalf("material ids = %v", ids)
	}
	data := map[string]any{"updatedResources": map[string]any{"userMysekaiHarvestMaps": []any{
		map[string]any{"mysekaiSiteId": 5, "userMysekaiSiteHarvestResourceDrops": []any{
			map[string]any{"resourceType": "mysekai_material", "resourceId": 17, "positionX": 1, "positionZ": 1},
			map[string]any{"resourceType": "mysekai_material", "resourceId": 11, "positionX": 2, "positionZ": 2},
			map[string]any{"resourceType": "mysekai_material", "resourceId": 12, "positionX": 3, "positionZ": 3},
		}},
	}}}
	filtered, matched, empty := FilterBirthdayPartyPayload(data, ids)
	if empty || !slices.Equal(matched, ids) {
		t.Fatalf("matched = %v, empty = %t", matched, empty)
	}
	maps := birthdayHarvestMaps(filtered)
	site, _ := mapStringAny(maps[0])
	if drops := anySlice(site["userMysekaiSiteHarvestResourceDrops"]); len(drops) != 2 {
		t.Fatalf("drops = %v", drops)
	}
}
