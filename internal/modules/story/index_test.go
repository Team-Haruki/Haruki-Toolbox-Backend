package story

import (
	"testing"
	"time"
)

func TestBuildAssetIndex(t *testing.T) {
	keys := []string{
		"startapp/live2d/model/v1/main/10_an/10an_normal/10an_normal_3.0_t05.model3",
		"startapp/live2d/model/v1/main/10_an/10an_normal/10an_normal_3.0_t05.physics3",
		// stale previous export next to the current one
		"startapp/live2d/model/v1/main/10_an/10an_normal/10an_normal_3.0_t04.model3",
		"startapp/live2d/model/v1/main/10_an/10an_normal/10an_normal_3.0_t04.physics3",
		"startapp/live2d/model/v2/main/02_saki/v2_02saki_casual/v2_02saki_clothes_t9.model3",
		"startapp/live2d/model/v2/main/02_saki/v2_02saki_casual/v2_02saki_clothes_t9.physics3",
		"startapp/live2d/model/v2/main/02_saki/v2_02saki_casual/v2_02saki_clothes_t10.model3",
		"startapp/live2d/model/v1/main/10_an/10an_normal/10an_normal_3.0_t05.2048/texture_00.png",
		"startapp/live2d/model/v2/main/15_nene/v2_15nene_casual/v2_15_nene_clothes_t08.model3",
		"startapp/live2d/model/v2/main/15_nene/v2_15nene_casual/motions/w-pure03-nod.motion3.json",
		"startapp/live2d/model/v1/sub/sub_sakaki02/sub_sakaki02_t03.model3",
		"startapp/live2d/model/v2/collabo/21_miku/v2_clb01_21miku/v2_clb01_21miku_t01.model3",
		"startapp/live2d/model/v1/sub/sub_emu_princess/sub_emu_princess.model3",
		"startapp/live2d/motion/v1/main/10_an/10an_motion_base/motion/w-cute02-guts.motion3.json",
		"startapp/live2d/motion/v2/main/15_nene/v2_15nene_motion_base/facial/face_smile_01.motion3.json",
		"startapp/live2d/motion/v1/sub/sub_sakaki_motion_base/motion/s-common-pose01.motion3.json",
		"startapp/live2d/motion/v1/collabo/21_miku/clb01_21miku_motion_base/motion/x.motion3.json",
		"ondemand/scenario/movie/01_leoneed_ed1/leoneed_ed1_logoanimation_se_fix.mp4",
		"ondemand/scenario/movie/01_leoneed_ed1/moviebundlebuilddata.json",
		"ondemand/movie/idol_opening/idol.mp4",
		"startapp/live2d/model/v2/sub/v2_20mizuki_culture_back/v2_20mizuki_culture_back_t04.model3",
		"startapp/live2d/model/v2/main/20_mizuki/v2_20mizuki_culture/v2_20mizuki_culture_t08.model3",
		"startapp/live2d/motion/v2/main/20_mizuki/v2_20mizuki_motion_base/motion/w-normal-pose01.motion3.json",
		"startapp/live2d/motion/v2/sub/v2_20mizuki_back_motion_base/motion/w-back16-sigh.motion3.json",
		"startapp/sound/scenario/se/se_pack00001_b/se_door.mp3",
		"startapp/sound/scenario/se/se/se_door.mp3",
		"startapp/sound/scenario/se/se_pack00001/se_bell.mp3",
	}
	idx := buildAssetIndex(keys, time.Unix(0, 0))
	an := idx.Models["10an_normal"]
	if back, front := idx.Models["v2_20mizuki_culture_back"], idx.Models["v2_20mizuki_culture"]; back.MotionBase != "startapp/live2d/motion/v2/sub/v2_20mizuki_back_motion_base" ||
		front.MotionBase != "startapp/live2d/motion/v2/main/20_mizuki/v2_20mizuki_motion_base" {
		t.Fatalf("back/front motion bases = %q / %q", back.MotionBase, front.MotionBase)
	}
	if saki := idx.Models["v2_02saki_casual"]; saki.Model3 != "v2_02saki_clothes_t10.model3" || saki.Physics != "" {
		t.Fatalf("saki = %+v (newest model3, and no physics of another version)", saki)
	}
	if an.Model3 != "10an_normal_3.0_t05.model3" || an.Physics != "10an_normal_3.0_t05.physics3" ||
		an.MotionBase != "startapp/live2d/motion/v1/main/10_an/10an_motion_base" {
		t.Fatalf("10an_normal = %+v", an)
	}
	nene := idx.Models["v2_15nene_casual"]
	if nene.MotionBase != "startapp/live2d/motion/v2/main/15_nene/v2_15nene_motion_base" || len(nene.Motions) != 1 || nene.Motions[0] != "w-pure03-nod" || nene.Physics != "" {
		t.Fatalf("v2_15nene_casual = %+v", nene)
	}
	if got := idx.Models["sub_sakaki02"].MotionBase; got != "startapp/live2d/motion/v1/sub/sub_sakaki_motion_base" {
		t.Fatalf("sub_sakaki02 base = %q", got)
	}
	if got := idx.Models["v2_clb01_21miku"].MotionBase; got != "startapp/live2d/motion/v1/collabo/21_miku/clb01_21miku_motion_base" {
		t.Fatalf("v2 fallback base = %q", got)
	}
	if got := idx.Models["sub_emu_princess"].MotionBase; got != "" {
		t.Fatalf("unmatched base = %q", got)
	}
	if idx.Se["se_door"] != "startapp/sound/scenario/se/se/se_door.mp3" || idx.Se["se_bell"] != "startapp/sound/scenario/se/se_pack00001/se_bell.mp3" {
		t.Fatalf("se = %v", idx.Se)
	}
	if idx.Movies["01_leoneed_ed1"] != "ondemand/scenario/movie/01_leoneed_ed1/leoneed_ed1_logoanimation_se_fix.mp4" || idx.Movies["idol_opening"] != "ondemand/movie/idol_opening/idol.mp4" {
		t.Fatalf("movies = %v", idx.Movies)
	}
	if idx.Version == "" {
		t.Fatal("empty version")
	}
}
