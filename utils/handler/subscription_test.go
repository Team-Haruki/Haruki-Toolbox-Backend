package handler

import "testing"

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
