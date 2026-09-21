package gamedata

import (
	"bytes"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/mysekairestore"
	"testing"
)

func TestHarvestRestoreAcrossResponseShapes(t *testing.T) {
	const raw = `[[5,[[111,-3,7,20,"spawned",null]],[["mysekai_item",24,-3,7,10,13,"before_drop",2,null]]]]`
	for _, region := range []string{"cn", "tw", "kr"} {
		r := newTestRow(t, catalog.Mysekai(), map[string]string{"updatedResources.userMysekaiHarvestMaps": raw}, "")
		r.Server = region
		r.restorer, _ = mysekairestore.New(map[string]string{"cn": "../../../data/suite_user_cn_6.4.0.avsc"})
		if region == "tw" {
			r.restorer, _ = mysekairestore.New(map[string]string{"tw": "../../../data/suite_user_cn_6.4.0.avsc"})
		}
		for _, keys := range [][]string{nil, {"updatedResources"}, {"updatedResources.userMysekaiHarvestMaps"}} {
			body, err := r.MysekaiBody(keys)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(body, []byte(`"resourceType"`)) != (region != "kr") {
				t.Fatalf("region %s keys %v: %s", region, keys, body)
			}
		}
		for _, v := range r.byColumn {
			if string(v) != raw {
				t.Fatal("mutated stored bytes")
			}
		}
	}
	r := newTestRow(t, catalog.Suite(), map[string]string{"userMysekaiHarvestMaps": raw}, "")
	r.Server = "cn"
	r.restorer, _ = mysekairestore.New(map[string]string{"cn": "../../../data/suite_user_cn_6.4.0.avsc"})
	body, err := r.SuiteBody([]string{"userMysekaiHarvestMaps"}, true)
	if err != nil || !bytes.Contains(body, []byte(`"mysekaiSiteId":5`)) {
		t.Fatalf("suite: %s %v", body, err)
	}
}
func TestBadHarvestReadReturnsError(t *testing.T) {
	r := newTestRow(t, catalog.Mysekai(), map[string]string{"updatedResources.userMysekaiHarvestMaps": `[[5]]`}, "")
	r.Server = "cn"
	r.restorer, _ = mysekairestore.New(map[string]string{"cn": "../../../data/suite_user_cn_6.4.0.avsc"})
	for range 2 {
		if _, err := r.MysekaiBody(nil); err == nil {
			t.Fatal("bad stored data silently accepted")
		}
	}
}
