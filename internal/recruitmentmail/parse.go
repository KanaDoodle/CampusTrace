// Package recruitmentmail parses user-supplied notices locally. No URLs, attachments
// or instructions in a message are executed. Every extracted field is a suggestion.
package recruitmentmail

import (
	"encoding/base64"
	"errors"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxBytes = 128 * 1024

type Parsed struct {
	Subject        string     `json:"subject"`
	From           string     `json:"from"`
	MessageID      string     `json:"message_id"`
	Text           string     `json:"text"`
	Kind           string     `json:"kind"`
	Reference      string     `json:"reference"`
	ScheduledAt    *time.Time `json:"scheduled_at,omitempty"`
	TimeCandidates []string   `json:"time_candidates"`
	Warnings       []string   `json:"warnings"`
}

func Parse(content, format string) (Parsed, error) {
	p := Parsed{Kind: "UNKNOWN", Warnings: []string{}, TimeCandidates: []string{}}
	if len(content) == 0 || len(content) > MaxBytes || !utf8.ValidString(content) || strings.ContainsRune(content, '\x00') {
		return p, errors.New("invalid notice size or encoding")
	}
	if format != "text" && format != "eml" {
		return p, errors.New("unsupported notice format")
	}
	if format == "eml" {
		m, e := mail.ReadMessage(strings.NewReader(content))
		if e != nil {
			return p, e
		}
		decoder := &mime.WordDecoder{CharsetReader: func(c string, r io.Reader) (io.Reader, error) { return charset.NewReaderLabel(c, r) }}
		p.Subject, e = decoder.DecodeHeader(m.Header.Get("Subject"))
		if e != nil {
			return p, e
		}
		p.From, _ = decoder.DecodeHeader(m.Header.Get("From"))
		p.MessageID = strings.TrimSpace(m.Header.Get("Message-ID"))
		p.Text, e = bodyText(m.Header.Get("Content-Type"), m.Header.Get("Content-Transfer-Encoding"), m.Body, 0)
		if e != nil {
			return p, e
		}
		p.Warnings = append(p.Warnings, "仅读取邮件正文；附件、远程图片与链接未读取。")
	} else {
		p.Text = content
		p.Subject = strings.SplitN(strings.TrimSpace(content), "\n", 2)[0]
	}
	p.Text = strings.TrimSpace(strings.ReplaceAll(p.Text, "\r\n", "\n"))
	p.Subject = strings.TrimSpace(p.Subject)
	if len(p.Subject) > 1000 || len(p.From) > 1000 || len(p.MessageID) > 1000 || len(p.Text) > MaxBytes || p.Text == "" || !utf8.ValidString(p.Text) {
		return p, errors.New("invalid decoded notice")
	}
	text := p.Subject + "\n" + p.Text
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(text, "改期") || strings.Contains(text, "调整时间") || strings.Contains(lower, "reschedul"):
		p.Kind = "RESCHEDULE"
	case strings.Contains(text, "未通过") || strings.Contains(text, "遗憾") || strings.Contains(lower, "unsuccessful"):
		p.Kind = "REJECTED"
	case strings.Contains(text, "录用通知") || strings.Contains(lower, "offer letter"):
		p.Kind = "OFFER"
	case strings.Contains(text, "面试") || strings.Contains(lower, "interview"):
		p.Kind = "INTERVIEW"
	case strings.Contains(text, "笔试") || strings.Contains(lower, "assessment"):
		p.Kind = "OA"
	}
	ref := regexp.MustCompile(`(?i)(?:申请编号|投递编号|应聘编号|application\s*(?:id|number)|reference)\s*[:：#]?\s*([a-z0-9][a-z0-9_-]{2,79})`).FindStringSubmatch(text)
	if len(ref) > 1 {
		p.Reference = ref[1]
	}
	// Only full dates with an explicit clock time are proposed. Multiple instants
	// (often original/new time or booking deadline) require a human to choose.
	re := regexp.MustCompile(`(?i)(20\d{2})[-/年](\d{1,2})[-/月](\d{1,2})日?\s*[T\s]\s*(\d{1,2})[:：](\d{2})(?::\d{2})?(?:\s*(Z\b|[+-]\d{2}:\d{2}|UTC\b|GMT\b))?`)
	seen := map[string]bool{}
	unsafeTime := false
	for _, indexes := range re.FindAllStringSubmatchIndex(text, -1) {
		m := make([]string, len(indexes)/2)
		for i := range m {
			if indexes[2*i] >= 0 {
				m[i] = text[indexes[2*i]:indexes[2*i+1]]
			}
		}
		after := strings.TrimSpace(text[indexes[1]:])
		zone := strings.ToUpper(m[6])
		if (zone == "" && regexp.MustCompile(`^(?:[A-Z]{2,5}\b|[A-Za-z_]+/[A-Za-z_]+\b|[+-]\d)`).MatchString(after)) || ((zone == "UTC" || zone == "GMT") && regexp.MustCompile(`^[+-]\d`).MatchString(after)) {
			unsafeTime = true
			p.Warnings = append(p.Warnings, "正文包含未支持或有歧义的时区，请核对后人工填写时间。")
			continue
		}
		if replyDeadline(text, indexes[0], indexes[1]) {
			unsafeTime = true
		}
		nums := []int{}
		for _, s := range m[1:6] {
			n, _ := strconv.Atoi(s)
			nums = append(nums, n)
		}
		loc := time.FixedZone("Asia/Shanghai", 8*3600)
		value := m[0]
		if zone == "Z" || zone == "UTC" || zone == "GMT" {
			loc = time.UTC
		} else if z := regexp.MustCompile(`([+-])(\d{2}):(\d{2})$`).FindStringSubmatch(zone); len(z) > 0 {
			h, _ := strconv.Atoi(z[2])
			mi, _ := strconv.Atoi(z[3])
			if h > 14 || mi > 59 {
				continue
			}
			off := (h*60 + mi) * 60
			if z[1] == "-" {
				off = -off
			}
			loc = time.FixedZone("", off)
		}
		at := time.Date(nums[0], time.Month(nums[1]), nums[2], nums[3], nums[4], 0, 0, loc)
		if exact, e := time.Parse(time.RFC3339, value); e == nil {
			at = exact
		}
		if at.Year() != nums[0] || int(at.Month()) != nums[1] || at.Day() != nums[2] || at.Hour() != nums[3] || at.Minute() != nums[4] {
			continue
		}
		key := at.UTC().Format(time.RFC3339)
		if !seen[key] {
			p.TimeCandidates = append(p.TimeCandidates, key)
			seen[key] = true
		}
	}
	if len(p.TimeCandidates) == 1 && !unsafeTime {
		at, _ := time.Parse(time.RFC3339, p.TimeCandidates[0])
		p.ScheduledAt = &at
	} else if len(p.TimeCandidates) > 1 {
		p.Warnings = append(p.Warnings, "正文包含多个时间，请核对活动时间与回复截止；改期请明确选择新时间。")
	}
	if unsafeTime {
		p.Warnings = append(p.Warnings, "日期可能表示回复截止或时区尚未核清，未自动作为活动时间预填。")
	}
	if len(p.TimeCandidates) == 0 {
		p.Warnings = append(p.Warnings, "没有找到唯一的完整日期与时间，请人工填写；未写时区的日期按北京时间解释。")
	}
	p.Warnings = append(p.Warnings, "通知类型、岗位和时间均需确认后才会更新申请。")
	return p, nil
}

// A deadline is retained as a candidate, but is never silently turned into an
// appointment. Inspect only the adjacent sentence, with a conservative window.
func replyDeadline(text string, start, end int) bool {
	before := text[:start]
	after := text[end:]
	for _, sep := range []string{"。", "；", ";", "\n"} {
		if i := strings.LastIndex(before, sep); i >= 0 {
			before = before[i+len(sep):]
		}
		if i := strings.Index(after, sep); i >= 0 {
			after = after[:i]
		}
	}
	pre, post := []rune(before), []rune(after)
	if len(pre) > 32 {
		pre = pre[len(pre)-32:]
	}
	if len(post) > 24 {
		post = post[:24]
	}
	return regexp.MustCompile(`(?i)(?:回复|确认|报名|提交|预约).{0,6}(?:截止|期限)|(?:reply|respond|rsvp)\s+by\s*$`).MatchString(string(pre)) || regexp.MustCompile(`^(?:\s|,|，)*(?:前|之前).{0,8}(?:回复|确认|提交|报名|反馈)`).MatchString(string(post))
}

func bodyText(contentType, encoding string, r io.Reader, depth int) (string, error) {
	if depth > 4 {
		return "", errors.New("mime nesting limit")
	}
	kind, params, e := mime.ParseMediaType(contentType)
	if contentType == "" {
		kind = "text/plain"
		e = nil
	}
	if e != nil {
		return "", e
	}
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		r = base64.NewDecoder(base64.StdEncoding, r)
	case "quoted-printable":
		r = quotedprintable.NewReader(r)
	case "", "7bit", "8bit", "binary":
	default:
		return "", errors.New("unsupported transfer encoding")
	}
	if strings.HasPrefix(kind, "multipart/") {
		if params["boundary"] == "" {
			return "", errors.New("missing mime boundary")
		}
		mr := multipart.NewReader(r, params["boundary"])
		plain, rich := "", ""
		for i := 0; i < 32; i++ {
			part, err := mr.NextPart()
			if err == io.EOF {
				if plain != "" {
					return plain, nil
				}
				return rich, nil
			}
			if err != nil {
				return "", err
			}
			disposition, _, _ := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
			if disposition == "attachment" {
				part.Close()
				continue
			}
			t, err := bodyText(part.Header.Get("Content-Type"), part.Header.Get("Content-Transfer-Encoding"), part, depth+1)
			part.Close()
			if err != nil {
				return "", err
			}
			if strings.HasPrefix(part.Header.Get("Content-Type"), "text/html") {
				rich += t + "\n"
			} else {
				plain += t + "\n"
			}
			if len(plain)+len(rich) > MaxBytes {
				return "", errors.New("decoded size limit")
			}
		}
		return "", errors.New("mime part limit")
	}
	if kind != "text/plain" && kind != "text/html" {
		return "", nil
	}
	if c := params["charset"]; c != "" {
		r, e = charset.NewReaderLabel(c, r)
		if e != nil {
			return "", e
		}
	}
	b, e := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if e != nil || len(b) > MaxBytes {
		return "", errors.New("decoded size limit")
	}
	if kind == "text/plain" {
		return string(b), nil
	}
	z := html.NewTokenizer(strings.NewReader(string(b)))
	var out strings.Builder
	hidden := 0
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			if z.Err() != io.EOF {
				return "", z.Err()
			}
			break
		}
		t := z.Token()
		if tt == html.StartTagToken && (t.Data == "script" || t.Data == "style") {
			hidden++
		}
		if tt == html.EndTagToken && (t.Data == "script" || t.Data == "style") && hidden > 0 {
			hidden--
		}
		if hidden == 0 && tt == html.TextToken {
			out.WriteString(t.Data)
			out.WriteByte('\n')
		}
	}
	return out.String(), nil
}
