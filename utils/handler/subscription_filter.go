package handler

import (
	"fmt"
	"slices"
	"strings"
)

func FilterBirthdayPartyPayload(data map[string]any, materialIDs []int) (map[string]any, []int, bool) {
	targets := make(map[int]struct{}, len(materialIDs))
	for _, id := range materialIDs {
		if id > 0 {
			targets[id] = struct{}{}
		}
	}

	filteredMaps := make([]any, 0)
	matchedIDs := make([]int, 0, len(targets))
	matchedSet := make(map[int]struct{}, len(targets))
	for _, rawMap := range birthdayHarvestMaps(data) {
		siteMap, ok := mapStringAny(rawMap)
		if !ok {
			continue
		}
		drops := anySlice(siteMap["userMysekaiSiteHarvestResourceDrops"])
		fixtures := anySlice(siteMap["userMysekaiSiteHarvestFixtures"])

		fixtureIDsByPosition := make(map[string]map[int]struct{})
		positionsByFixtureID := make(map[int]map[string]struct{})
		for _, rawFixture := range fixtures {
			fixture, ok := mapStringAny(rawFixture)
			if !ok {
				continue
			}
			fixtureID := birthdayFixtureID(fixture)
			if fixtureID <= 0 {
				continue
			}
			posKey := birthdayPosKey(fixture)
			if posKey == "" {
				continue
			}
			if fixtureIDsByPosition[posKey] == nil {
				fixtureIDsByPosition[posKey] = make(map[int]struct{})
			}
			fixtureIDsByPosition[posKey][fixtureID] = struct{}{}
			if positionsByFixtureID[fixtureID] == nil {
				positionsByFixtureID[fixtureID] = make(map[string]struct{})
			}
			positionsByFixtureID[fixtureID][posKey] = struct{}{}
		}

		matchedPositions := make(map[string]struct{})
		matchedFixtureIDs := make(map[int]struct{})
		siteMatched := false
		for _, rawDrop := range drops {
			drop, ok := mapStringAny(rawDrop)
			if !ok {
				continue
			}
			resourceType := normalizeBirthdayResourceType(stringFromAny(firstPresent(drop, "resourceType", "type")))
			resourceID := intFromAny(firstPresent(drop, "resourceId", "id"))
			if resourceType != "mysekai_material" {
				continue
			}
			if _, ok := targets[resourceID]; !ok {
				continue
			}
			siteMatched = true
			if _, ok := matchedSet[resourceID]; !ok {
				matchedSet[resourceID] = struct{}{}
				matchedIDs = append(matchedIDs, resourceID)
			}
			if key := birthdayPosKey(drop); key != "" {
				matchedPositions[key] = struct{}{}
				for fixtureID := range fixtureIDsByPosition[key] {
					matchedFixtureIDs[fixtureID] = struct{}{}
				}
			}
			if fixtureID := birthdayFixtureID(drop); fixtureID > 0 {
				matchedFixtureIDs[fixtureID] = struct{}{}
			}
		}
		if !siteMatched {
			continue
		}

		keptPositions := make(map[string]struct{}, len(matchedPositions))
		for posKey := range matchedPositions {
			keptPositions[posKey] = struct{}{}
		}
		for fixtureID := range matchedFixtureIDs {
			for posKey := range positionsByFixtureID[fixtureID] {
				keptPositions[posKey] = struct{}{}
			}
		}

		keptDrops := make([]any, 0)
		for _, rawDrop := range drops {
			drop, ok := mapStringAny(rawDrop)
			if !ok {
				continue
			}
			keep := false
			resourceType := normalizeBirthdayResourceType(stringFromAny(firstPresent(drop, "resourceType", "type")))
			resourceID := intFromAny(firstPresent(drop, "resourceId", "id"))
			if resourceType == "mysekai_material" {
				_, keep = targets[resourceID]
			}
			if !keep {
				if _, ok := keptPositions[birthdayPosKey(drop)]; ok {
					keep = true
				}
			}
			if !keep {
				if fixtureID := birthdayFixtureID(drop); fixtureID > 0 {
					_, keep = matchedFixtureIDs[fixtureID]
				}
			}
			if keep {
				keptDrops = append(keptDrops, cloneMap(drop))
			}
		}

		keptFixtures := make([]any, 0)
		for _, rawFixture := range fixtures {
			fixture, ok := mapStringAny(rawFixture)
			if !ok {
				continue
			}
			if _, ok := keptPositions[birthdayPosKey(fixture)]; ok {
				keptFixtures = append(keptFixtures, cloneMap(fixture))
				continue
			}
			if fixtureID := birthdayFixtureID(fixture); fixtureID > 0 {
				if _, ok := matchedFixtureIDs[fixtureID]; ok {
					keptFixtures = append(keptFixtures, cloneMap(fixture))
				}
			}
		}

		copiedMap := cloneMap(siteMap)
		copiedMap["userMysekaiSiteHarvestResourceDrops"] = keptDrops
		copiedMap["userMysekaiSiteHarvestFixtures"] = keptFixtures
		filteredMaps = append(filteredMaps, copiedMap)
	}

	slices.Sort(matchedIDs)
	filtered := map[string]any{
		"updatedResources": map[string]any{
			"userMysekaiHarvestMaps": filteredMaps,
		},
		"upload_time": data["upload_time"],
		"server":      data["server"],
		"source":      "toolbox_live",
	}
	return filtered, matchedIDs, len(matchedIDs) == 0
}

func materialIDsFromNames(materials []string) []int {
	ids := make([]int, 0, len(materials))
	seen := make(map[int]struct{}, len(materials))
	for _, name := range materials {
		id := 0
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "diamond", "mysekai_material_12":
			id = 12
		case "yuugiri", "yugiri", "mysekai_material_5":
			id = 5
		case "clover", "mysekai_material_20":
			id = 20
		}
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func birthdayHarvestMaps(data map[string]any) []any {
	updated, ok := mapStringAny(data["updatedResources"])
	if !ok {
		return nil
	}
	return anySlice(updated["userMysekaiHarvestMaps"])
}

func normalizeBirthdayResourceType(resourceType string) string {
	switch strings.ToLower(strings.TrimSpace(resourceType)) {
	case "mysekai_material":
		return "mysekai_material"
	case "material":
		return "material"
	case "item", "mysekai_item":
		return "mysekai_item"
	case "fixture", "mysekai_fixture":
		return "mysekai_fixture"
	case "music_record", "mysekai_music_record":
		return "mysekai_music_record"
	default:
		return strings.TrimSpace(resourceType)
	}
}

func birthdayPosKey(item map[string]any) string {
	xRaw := firstPresent(item, "positionX", "position_x")
	zRaw := firstPresent(item, "positionZ", "position_z")
	if xRaw == nil || zRaw == nil {
		return ""
	}
	x := floatFromAny(xRaw)
	z := floatFromAny(zRaw)
	return fmt.Sprintf("%.3f_%.3f", x, z)
}

func birthdayFixtureID(item map[string]any) int {
	return intFromAny(firstPresent(
		item,
		"mysekaiSiteHarvestFixtureId",
		"mysekaiSiteHarvestFixtureID",
		"mysekai_site_harvest_fixture_id",
		"mysekaiFixtureId",
		"mysekaiFixtureID",
		"mysekai_fixture_id",
	))
}
