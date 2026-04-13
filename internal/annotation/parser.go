// @index 커스텀 어노테이션 파서. 주석에서 @intent, @domainRule, @index 등 구조화된 태그를 추출한다.
package annotation

import (
	"strings"

	"github.com/imtaebin/code-context-graph/internal/model"
)

var knownTags = map[string]model.TagKind{
	"param":      model.TagParam,
	"return":     model.TagReturn,
	"see":        model.TagSee,
	"intent":     model.TagIntent,
	"domainRule": model.TagDomainRule,
	"sideEffect": model.TagSideEffect,
	"mutates":    model.TagMutates,
	"requires":   model.TagRequires,
	"ensures":    model.TagEnsures,
	"index":      model.TagIndex,
}

type Parser struct{}

func NewParser() *Parser {
	return &Parser{}
}

// Parse extracts structured annotations from normalized comment text.
// Supports standard tags and custom AI/business context tags.
//
// @param text normalizer output with comment prefixes stripped
// @return Annotation with Summary, Context, and DocTag list
// @intent extract machine-readable metadata from developer comments
// @domainRule first non-tag line becomes Summary, second becomes Context
// @domainRule recognized tags: param, return, see, intent, domainRule, sideEffect, mutates, requires, ensures
// @domainRule unknown tags are silently ignored
// Parse extracts structured annotations from normalized comment text.
// Returns the annotation and a slice of unrecognized tag names (e.g. ["domainrule"] for a typo).
// Callers that do not need warnings can discard the second return value.
func (p *Parser) Parse(text string) (*model.Annotation, []string) {
	ann := &model.Annotation{RawText: text}

	lines := strings.Split(text, "\n")
	ordinals := make(map[model.TagKind]int)
	var warnings []string

	// Phase 1: 첫 @태그 이전 줄들을 단락(빈 줄 구분)으로 분류
	// 첫 단락 → Summary, 둘째 단락 → Context
	var paragraphs [][]string
	var currentPara []string
	firstTagIdx := len(lines)

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "@") {
			firstTagIdx = i
			break
		}
		if trimmed == "" {
			if len(currentPara) > 0 {
				paragraphs = append(paragraphs, currentPara)
				currentPara = nil
			}
		} else {
			currentPara = append(currentPara, trimmed)
		}
	}
	if len(currentPara) > 0 {
		paragraphs = append(paragraphs, currentPara)
	}

	if len(paragraphs) > 0 {
		ann.Summary = strings.Join(paragraphs[0], "\n")
	}
	if len(paragraphs) > 1 {
		ann.Context = strings.Join(paragraphs[1], "\n")
	}

	// Phase 2: 태그 파싱. continuation 줄은 \n으로 이어붙임
	inTag := false
	for _, line := range lines[firstTagIdx:] {
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			inTag = false
			continue
		}

		if strings.HasPrefix(trimmed, "@") {
			parsed, unknown := p.parseTagLine(trimmed, ordinals)
			if unknown != "" {
				warnings = append(warnings, unknown)
			}
			if parsed != nil {
				ann.Tags = append(ann.Tags, *parsed)
				inTag = true
			} else {
				inTag = false
			}
			continue
		}

		if inTag && len(ann.Tags) > 0 {
			ann.Tags[len(ann.Tags)-1].Value += "\n" + trimmed
		}
	}

	return ann, warnings
}

// parseTagLine parses a single @tag line.
// Returns (tag, "") on success, (nil, tagName) when the tag is unknown.
func (p *Parser) parseTagLine(line string, ordinals map[model.TagKind]int) (*model.DocTag, string) {
	rest := line[1:]
	parts := strings.SplitN(rest, " ", 2)
	tagName := parts[0]

	kind, known := knownTags[tagName]
	if !known {
		return nil, tagName
	}

	value := ""
	if len(parts) > 1 {
		value = strings.TrimSpace(parts[1])
	}

	tag := &model.DocTag{
		Kind:    kind,
		Ordinal: ordinals[kind],
	}
	ordinals[kind]++

	if kind == model.TagParam {
		paramParts := strings.SplitN(value, " ", 2)
		if paramParts[0] == "" {
			return nil, ""
		}
		tag.Name = paramParts[0]
		if len(paramParts) > 1 {
			tag.Value = strings.TrimSpace(paramParts[1])
		}
	} else {
		tag.Value = value
	}

	return tag, ""
}
