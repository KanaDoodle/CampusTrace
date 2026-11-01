package agent

import (
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"regexp"
	"strconv"
)

// Only explicit remaining-time phrases change an existing constraint. The
// normalized minutes are shown in the review before any model request.
func NormalizePreparationInput(in d.PreparationInput) d.PreparationInput {
	text := in.Feedback
	if text == "" && in.Minutes == 0 {
		text = in.Goal
	}
	re := regexp.MustCompile(`(?:只剩|剩余|只有|仅有|可用|还剩)\s*([0-9]{1,4}|半|一|二|两|三|四|五|六|七|八|九|十|[二三四五六七八九]?十[一二三四五六七八九]?)\s*(小时|分钟)`)
	m := re.FindStringSubmatch(text)
	if len(m) < 3 {
		return in
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		digits := map[rune]int{'一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
		if m[1] == "半" {
			if m[2] == "小时" {
				in.Minutes = 30
			}
			return in
		}
		rs := []rune(m[1])
		n = 0
		for i, r := range rs {
			if r == '十' {
				tens := 1
				if i > 0 {
					tens = digits[rs[i-1]]
				}
				n = tens * 10
			} else if i+1 == len(rs) {
				n += digits[r]
			}
		}
	}
	if m[2] == "小时" {
		n *= 60
	}
	if n >= 5 && n <= 1440 {
		in.Minutes = n
	}
	return in
}
