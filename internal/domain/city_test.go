package domain

import "testing"

func TestCitiesMatchAliasesButDoNotGuessUnknownPlaces(t *testing.T) {
	for _, pair := range [][2]string{{"Shanghai", "上海市"}, {"Hangzhou City", "杭州"}, {"BEIJING", "北京市"}, {"Shenzhen", "深圳"}, {"Xi'an", "西安市"}} {
		if !CityAlternatives(pair[0], []string{pair[1]}) {
			t.Fatal(pair)
		}
	}
	if CityAlternatives("北京市|上海市", []string{"Hangzhou"}) || CityAlternatives("", []string{""}) || CityAlternatives("浙江省", []string{"杭州"}) {
		t.Fatal("different or empty location matched")
	}
}

func TestCanonicalCitiesPreserveRawLocationsAndGeography(t *testing.T) {
	cases := map[string]string{
		"杭州市-余杭区": "杭州", "江苏省-南京市-建邺区": "南京",
		"上海市-上海市-嘉定区": "上海", "江苏省-苏州": "苏州",
		"江苏省-苏州市-昆山市": "苏州", "中国 广东省 深圳市": "深圳",
		"广东省深圳市南山区": "深圳", "中国香港": "香港", "Hong Kong": "香港",
		"浙江省": "浙江省", "桐庐县": "桐庐县", "全国": "全国", "海外": "海外",
		"用 Go 在上海市实现服务": "用 go 在上海市实现服务", "项目-北京市-性能优化": "项目-北京市-性能优化",
	}
	for input, want := range cases {
		if got := CanonicalCity(input); got != want {
			t.Errorf("%q: got %q want %q", input, got, want)
		}
	}
	raw := []string{"北京市 / 上海市", "Beijing", "上海市、杭州市-余杭区", "", "待定"}
	got := CanonicalCities(raw)
	if len(got) != 3 || got[0] != "北京" || got[1] != "上海" || got[2] != "杭州" {
		t.Fatal(got)
	}
	if raw[0] != "北京市 / 上海市" {
		t.Fatal("source locations modified")
	}
	if !CityAlternatives("上海/北京", []string{"江苏省-南京市-建邺区", "中国 北京市 海淀区"}) || CityAlternatives("杭州", []string{"浙江省", "桐庐县", "全国"}) {
		t.Fatal("wrong city preference match")
	}
	if CitySetKey("Shanghai / 北京市 / 上海市") != CitySetKey("北京|上海") {
		t.Fatal("equivalent location sets differ")
	}
}
