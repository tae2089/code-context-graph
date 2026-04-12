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
}

type Parser struct{}

func NewParser() *Parser {
	return &Parser{}
}

func (p *Parser) Parse(text string) (*model.Annotation, error) {
	ann := &model.Annotation{
		RawText: text,
	}

	lines := strings.Split(text, "\n")
	ordinals := make(map[model.TagKind]int)
	var currentTag *model.DocTag

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			continue
		}

		if strings.HasPrefix(trimmed, "@") {
			currentTag = p.parseTagLine(trimmed, ordinals)
			if currentTag != nil {
				ann.Tags = append(ann.Tags, *currentTag)
			}
			continue
		}

		if currentTag != nil {
			lastIdx := len(ann.Tags) - 1
			ann.Tags[lastIdx].Value += " " + trimmed
			continue
		}

		if ann.Summary == "" {
			ann.Summary = trimmed
		} else if ann.Context == "" {
			ann.Context = trimmed
		}
	}

	return ann, nil
}

func (p *Parser) parseTagLine(line string, ordinals map[model.TagKind]int) *model.DocTag {
	rest := line[1:]
	parts := strings.SplitN(rest, " ", 2)
	tagName := parts[0]

	kind, known := knownTags[tagName]
	if !known {
		return nil
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
		tag.Name = paramParts[0]
		if len(paramParts) > 1 {
			tag.Value = strings.TrimSpace(paramParts[1])
		}
	} else {
		tag.Value = value
	}

	return tag
}
