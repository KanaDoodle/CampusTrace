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
