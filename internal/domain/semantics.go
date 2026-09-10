package domain

import (
	"errors"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata"
)

// ApplicationSignal describes a small, conservative temporal/negation grammar.
// It is independent of excerpt binding and of trust/freshness assessment.
type ApplicationSignal string

const (
	CurrentPositive    ApplicationSignal = "CURRENT_POSITIVE"
	CurrentNegative    ApplicationSignal = "CURRENT_NEGATIVE"
	HistoricalPositive ApplicationSignal = "HISTORICAL_POSITIVE"
	HistoricalNegative ApplicationSignal = "HISTORICAL_NEGATIVE"
	Resumed            ApplicationSignal = "RESUMED"
	Ambiguous          ApplicationSignal = "AMBIGUOUS"
)

var negativeApplication = regexp.MustCompile(`(?i)\b(applications? (are |is |have |has )?(currently )?(not (yet )?open|closed|ended|paused)|do not (apply|submit)|don['’]t (apply|submit)|hiring (is )?(currently )?paused|position (is )?closed)\b|招聘已截止|招聘已结束|停止招聘|暂停招聘|申请已结束|岗位已关闭|暂不接受申请|暂不开放申请|尚未开放申请|不要立即申请|请勿申请`)
var positiveApplication = regexp.MustCompile(`(?i)\b(apply now|applications? (are |is )?open|are open now)\b|立即申请|开始申请|开放申请|仍接受申请`)
var explicitCurrentOpen = regexp.MustCompile(`(?i)\b(applications? (are |is )?open|are open now)\b|本岗位仍接受申请|现正开放申请`)
var resumedApplication = regexp.MustCompile(`(?i)\b(hiring has resumed|applications? (are |is )?no longer closed|reopened|re-opened)\b|现已恢复|重新开放申请|恢复招聘`)
var uncertainApplication = regexp.MustCompile(`(?i)\b(do not assume|not necessarily|might|may be|if applications|applications? (are |is )?not closed)\b|是否|可能|部分岗位`)
var historicalApplication = regexp.MustCompile(`(?i)old page said|previous(ly)?|last year|used to|此前|曾经|去年|后现已恢复`)

func ApplicationMeaning(text string) ApplicationSignal {
	text = strings.ToLower(text)
	if uncertainApplication.MatchString(text) {
		return Ambiguous
	}
	resumed := resumedApplication.MatchString(text)
	historical := historicalApplication.MatchString(text)
	if historical {
		// Only an explicit temporal transition permits a current clause to replace history.
		for _, marker := range []string{"but ", "现重新", "现已"} {
			if at := strings.LastIndex(text, marker); at >= 0 {
				current := text[at:]
				if negativeApplication.MatchString(current) {
					return CurrentNegative
				}
				if resumedApplication.MatchString(current) || positiveApplication.MatchString(current) {
					return Resumed
				}
				return Ambiguous
			}
		}
		if positiveApplication.MatchString(text) {
			return HistoricalPositive
		}
		if negativeApplication.MatchString(text) || strings.Contains(text, "closed") {
			return HistoricalNegative
		}
		return Ambiguous
	}
	if resumed {
		remainder := resumedApplication.ReplaceAllString(text, "")
		if negativeApplication.MatchString(remainder) {
			return Ambiguous
		}
		return Resumed
	}
	if negativeApplication.MatchString(text) {
		if explicitCurrentOpen.MatchString(text) {
			return Ambiguous
		}
		return CurrentNegative
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.EqualFold(strings.TrimSpace(line), "closed: CLOSED") {
			return CurrentNegative
		}
	}
	if positiveApplication.MatchString(text) {
		return CurrentPositive
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.EqualFold(strings.TrimSpace(line), "apply: PRESENT") {
			return CurrentPositive
		}
	}
	return Ambiguous
}
func NegativeApplication(text string) bool { return ApplicationMeaning(text) == CurrentNegative }
func PositiveApplication(text string) bool {
	s := ApplicationMeaning(text)
	return s == CurrentPositive || s == Resumed
}
func (c Claim) ValidateMeaning() error {
	if (c.Type == "APPLY_ACTION" && c.Value == "PRESENT") || c.Type == "OPEN_SIGNAL" {
		if !PositiveApplication(c.Excerpt) {
			return errors.New("schema: excerpt does not support positive application claim")
		}
	}
	if c.Type == "CLOSED_SIGNAL" && !NegativeApplication(c.Excerpt) && !strings.EqualFold(strings.TrimSpace(c.Excerpt), "closed: CLOSED") {
		return errors.New("schema: excerpt does not support closure claim")
	}
	return nil
}

// DeadlineInstant returns the exclusive start of the next local day for a date,
// or the exact source instant for a timestamp carrying an explicit UTC offset.
func DeadlineInstant(value, timezone string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04 -07:00", "2006-01-02 15:04:05 -07:00"} {
		if v, err := time.Parse(layout, value); err == nil {
			return v, nil
		}
	}
	if timezone == "" {
		timezone = "Asia/Shanghai"
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, err
	}
	v, err := time.ParseInLocation("2006-01-02", value, loc)
	if err != nil {
		return time.Time{}, err
	}
	return v.AddDate(0, 0, 1), nil
}
