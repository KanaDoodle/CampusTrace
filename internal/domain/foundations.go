package domain

import "errors"

type FoundationSkill struct {
	Topic string `json:"topic"`
	Level string `json:"level"`
}

type FoundationTopic struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func FoundationTopics() []FoundationTopic {
	return []FoundationTopic{{"DATA_STRUCTURES", "数据结构"}, {"ALGORITHMS", "算法"}, {"OPERATING_SYSTEMS", "操作系统"}, {"COMPUTER_NETWORKS", "计算机网络"}, {"DATABASES", "数据库基础"}, {"COMPUTER_ARCHITECTURE", "计算机系统结构"}}
}

func FoundationLevelLabel(level string) string {
	switch level {
	case "UNDERSTAND":
		return "了解"
	case "FAMILIAR":
		return "熟悉"
	case "PRACTICED":
		return "有实践"
	}
	return ""
}

// Keep omission distinct from an explicit empty list for older clients. The
// canonical topic order also prevents reordered forms from changing evidence.
func (p *Profile) NormalizeFoundationSkills() error {
	if p.FoundationSkills == nil {
		return nil
	}
	topics := FoundationTopics()
	if len(p.FoundationSkills) > len(topics) {
		return errors.New("too many foundation assessments")
	}
	levels := map[string]string{}
	for _, skill := range p.FoundationSkills {
		if FoundationLevelLabel(skill.Level) == "" || levels[skill.Topic] != "" {
			return errors.New("invalid foundation assessment")
		}
		levels[skill.Topic] = skill.Level
	}
	out := []FoundationSkill{}
	for _, topic := range topics {
		if level := levels[topic.ID]; level != "" {
			out = append(out, FoundationSkill{topic.ID, level})
			delete(levels, topic.ID)
		}
	}
	if len(levels) != 0 {
		return errors.New("unknown foundation topic")
	}
	p.FoundationSkills = out
	return nil
}
