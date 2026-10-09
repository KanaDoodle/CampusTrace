package domain

import (
	"regexp"
	"sort"
	"strings"
)

var cityAliases = map[string]string{
	"shanghai": "上海", "hangzhou": "杭州", "beijing": "北京", "shenzhen": "深圳",
	"guangzhou": "广州", "chengdu": "成都", "wuhan": "武汉", "suzhou": "苏州",
	"nanjing": "南京", "tianjin": "天津", "chongqing": "重庆", "xiamen": "厦门",
	"hefei": "合肥", "changsha": "长沙", "zhengzhou": "郑州", "qingdao": "青岛",
	"jinan": "济南", "ningbo": "宁波", "fuzhou": "福州", "wuxi": "无锡",
	"xi'an": "西安", "xian": "西安", "zhuhai": "珠海", "dongguan": "东莞",
	"dalian": "大连", "shenyang": "沈阳", "harbin": "哈尔滨", "changchun": "长春",
	"kunming": "昆明", "nanning": "南宁", "haikou": "海口", "sanya": "三亚",
	"hong kong": "香港", "中国香港": "香港", "香港特别行政区": "香港",
	"macao": "澳门", "macau": "澳门", "中国澳门": "澳门", "澳门特别行政区": "澳门",
	"singapore": "新加坡",
}

var locationChoices = regexp.MustCompile(`[|/、,，;；\n]+`)
var locationPath = regexp.MustCompile(`\s*[-－—>·]\s*|\s+`)
var provincePrefix = regexp.MustCompile(`^[\p{Han}]{2,8}(?:省|自治区)`)
var cityAddress = regexp.MustCompile(`^([\p{Han}]{2,8}?)市(?:[\p{Han}]{1,12}(?:区|县|市))?$`)
var districtName = regexp.MustCompile(`^[\p{Han}]{1,12}(?:区|县)$`)

func knownCity(value string) string {
	if city, ok := cityAliases[value]; ok {
		return city
	}
	for _, city := range cityAliases {
		if value == city {
			return city
		}
	}
	return ""
}

func simpleCity(value string) string {
	v := strings.TrimSpace(strings.TrimSuffix(Normalize(value), " city"))
	if city, ok := cityAliases[v]; ok {
		return city
	}
	return strings.TrimSuffix(v, "市")
}

// CanonicalCity compares saved preferences with recruiting-site city names.
// Unknown names are retained; no province or district is guessed as a city.
func CanonicalCity(value string) string {
	v := Normalize(value)
	if city, ok := cityAliases[strings.TrimSuffix(v, " city")]; ok {
		return city
	}
	// Match an address-shaped value, not an arbitrary sentence mentioning a city.
	first := ""
	for _, part := range locationPath.Split(v, -1) {
		if part == "中国" || part == "中国大陆" || part == "china" {
			continue
		}
		part = strings.TrimPrefix(part, "中国")
		part = provincePrefix.ReplaceAllString(part, "")
		if match := cityAddress.FindStringSubmatch(part); match != nil {
			if first == "" {
				first = match[1]
			}
		} else if city := knownCity(part); city != "" {
			if first == "" {
				first = city
			}
		} else if part != "" && !districtName.MatchString(part) {
			return simpleCity(v)
		}
	}
	if first != "" {
		return first
	}
	return simpleCity(v)
}

// CanonicalCities is a display/comparison projection. Job source locations are
// never rewritten, so identity, fingerprints and original evidence stay intact.
func CanonicalCities(values []string) []string {
	cities := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		for _, choice := range locationChoices.Split(value, -1) {
			city := CanonicalCity(choice)
			if city == "" || city == "待定" || city == "未知" || city == "暂未填写" || city == "地点待确认" || city == "--" {
				continue
			}
			if !seen[city] {
				seen[city] = true
				cities = append(cities, city)
			}
		}
	}
	return cities
}

func CitySetKey(value string) string {
	set := map[string]bool{}
	for _, city := range CanonicalCities([]string{value}) {
		set[city] = true
	}
	cities := make([]string, 0, len(set))
	for city := range set {
		cities = append(cities, city)
	}
	sort.Strings(cities)
	return strings.Join(cities, "|")
}

func CityAlternatives(value string, choices []string) bool {
	for _, city := range CanonicalCities([]string{value}) {
		for _, choice := range CanonicalCities(choices) {
			if city == choice {
				return true
			}
		}
	}
	return false
}
