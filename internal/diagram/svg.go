package diagram

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

const (
	pkRowBackgroundColor = "#FFF3BF"
	uniqueRowColor       = "#D3F9D8"
	fkRowBackgroundColor = "#D0EBFF"
)

var (
	tableGroupRegex = regexp.MustCompile(`(?s)<g id="[^"]+"><g class="shape" >.*?</g></g>`)
	tableRectRegex  = regexp.MustCompile(`<rect x="([^"]+)" y="([^"]+)" width="([^"]+)" height="([^"]+)" class="shape stroke-N1 fill-N7"[^>]*/>`)
	headerRectRegex = regexp.MustCompile(`<rect x="[^"]+" y="[^"]+" width="[^"]+" height="36\.000000" class="class_header fill-N1" />`)
	rowMetaRegex    = regexp.MustCompile(`(?s)<text x="[^"]+" y="[^"]+" class="text fill-N2"[^>]*>([^<]*)</text><text x="[^"]+" y="[^"]+" class="text fill-AA2"[^>]*/><line x1="[^"]+" x2="[^"]+" y1="([^"]+)" y2="[^"]+" class=" stroke-N1"`)
	uniqueRowKeyRE  = regexp.MustCompile(`__sql2diagram_unique_\d+`)
)

func postProcessSVG(svg []byte) []byte {
	result := tableGroupRegex.ReplaceAllStringFunc(string(svg), func(group string) string {
		if !strings.Contains(group, "class_header") {
			return group
		}

		tableRectMatch := tableRectRegex.FindStringSubmatch(group)
		if len(tableRectMatch) < 4 {
			return group
		}

		headerRect := headerRectRegex.FindStringIndex(group)
		if len(headerRect) != 2 {
			return group
		}

		tableX := tableRectMatch[1]
		tableWidth := tableRectMatch[3]

		var backgrounds []string
		rowMatches := rowMetaRegex.FindAllStringSubmatch(group, -1)
		for _, row := range rowMatches {
			if len(row) < 3 {
				continue
			}

			rowLabel := html.UnescapeString(row[1])
			lineY, err := strconv.ParseFloat(row[2], 64)
			if err != nil {
				continue
			}

			rowBackgroundColor := rowBackgroundByConstraint(rowLabel)
			if rowBackgroundColor == "" {
				continue
			}

			backgrounds = append(backgrounds, fmt.Sprintf(
				`<rect x="%s" y="%.6f" width="%s" height="36.000000" class="row_constraint_bg" style="fill:%s;stroke:none;" />`,
				tableX, lineY-36, tableWidth, rowBackgroundColor,
			))
		}

		if len(backgrounds) == 0 {
			return group
		}

		insertAt := headerRect[1]
		return group[:insertAt] + strings.Join(backgrounds, "") + group[insertAt:]
	})

	result = uniqueRowKeyRE.ReplaceAllString(result, "UNIQUE")

	return []byte(result)
}

func rowBackgroundByConstraint(label string) string {
	switch {
	case strings.Contains(label, "(FK)"):
		return fkRowBackgroundColor
	case strings.Contains(label, "(PK)"):
		return pkRowBackgroundColor
	case strings.Contains(label, "(UNIQUE)"), strings.Contains(label, "(UQ)"):
		return uniqueRowColor
	default:
		return ""
	}
}

func uniqueConstraintRowKey(index int) string {
	return fmt.Sprintf("__sql2diagram_unique_%d", index+1)
}
