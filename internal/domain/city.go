package domain

import (
	"sort"
	"strings"
)

// CanonicalCity compares saved preferences with recruiting-site city names.
// Unknown names are retained; no province or district is guessed as a city.
func CanonicalCity(value string) string {
	v := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(Normalize(value), " city"), "市"))
	aliases := map[string]string{
		"shanghai": "上海", "hangzhou": "杭州", "beijing": "北京", "shenzhen": "深圳",
		"guangzhou": "广州", "chengdu": "成都", "wuhan": "武汉", "suzhou": "苏州",
		"nanjing": "南京", "tianjin": "天津", "chongqing": "重庆", "xiamen": "厦门",
		"hefei": "合肥", "changsha": "长沙", "zhengzhou": "郑州", "qingdao": "青岛",
		"jinan": "济南", "ningbo": "宁波", "fuzhou": "福州", "wuxi": "无锡",
		"xi'an": "西安", "xian": "西安", "zhuhai": "珠海", "dongguan": "东莞",
	}
	if city, ok := aliases[v]; ok {
		return city
	}
	return v
}

func CitySetKey(value string) string {
	set := map[string]bool{}
	for _, city := range strings.Split(value, "|") {
		if v := CanonicalCity(city); v != "" {
			set[v] = true
		}
	}
	cities := make([]string, 0, len(set))
	for city := range set {
		cities = append(cities, city)
	}
	sort.Strings(cities)
	return strings.Join(cities, "|")
}

func CityAlternatives(value string, choices []string) bool {
	for _, city := range strings.Split(value, "|") {
		if strings.TrimSpace(city) == "" {
			continue
		}
		for _, choice := range choices {
			if CanonicalCity(city) == CanonicalCity(choice) {
				return true
			}
		}
	}
	return false
}
