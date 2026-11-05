// Package defensestrategy limits offline optimization to teaching choices.
// Authorization, tools, source validation and budgets stay in their Go owners.
package defensestrategy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
)

type Profile struct {
	Schema   string `json:"schema"`
	Explain  string `json:"explain"`
	Followup string `json:"followup"`
	Reading  string `json:"reading"`
}

func Default() Profile { return Legacy() }

// Legacy describes the original prompt and historical runs without a snapshot.
func Legacy() Profile {
	return Profile{"defense-teaching-v1", "code-first", "current-gap", "gap-only"}
}
func (p Profile) Validate() error {
	if p.Schema != "defense-teaching-v1" || (p.Explain != "code-first" && p.Explain != "observation-first") || (p.Followup != "current-gap" && p.Followup != "contrast") || (p.Reading != "gap-only" && p.Reading != "read-one") {
		return fmt.Errorf("invalid teaching profile")
	}
	return nil
}
func Parse(raw []byte) (Profile, error) {
	var p Profile
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if len(raw) > 2048 {
		return p, fmt.Errorf("profile size exceeded")
	}
	if err := d.Decode(&p); err != nil {
		return p, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return p, fmt.Errorf("profile must contain one JSON object")
	}
	return p, p.Validate()
}
func (p Profile) ID() string {
	// Sorted JSON keys match the offline controller's canonical encoding.
	raw, _ := json.Marshal(map[string]string{"schema": p.Schema, "explain": p.Explain, "followup": p.Followup, "reading": p.Reading})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (p Profile) Instructions() string {
	if p == Legacy() {
		return ""
	}
	text := "本轮教学策略（仅改变表达与练习选择，既有规则继续适用）："
	if p.Explain == "observation-first" {
		text += "先指出本轮原回答中的具体表述，再解释对应代码和缺口。"
	} else {
		text += "先定位一个代码动作，再说明它与本轮回答的关系。"
	}
	if p.Followup == "contrast" {
		text += "追问时用一个对照问题区分正确机制与常见误解；根据本轮回答选择，不预设用户答错。"
	} else {
		text += "追问本轮尚未解释的一个具体动作。"
	}
	if p.Reading == "read-one" {
		text += "需要阅读时只定位一个明确片段和一个待回答问题；已经解释的内容不再安排。"
	} else {
		text += "只针对仍有依据缺口的内容安排阅读，依据足够时直接追问或结束。"
	}
	return text
}
