package assemble

import (
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// tableMarkdown converts OCR table HTML to a GFM table. Tables with merged
// cells (rowspan/colspan > 1) are returned as cleaned HTML instead, since GFM
// cannot express them. Empty or unparsable input returns "".
func tableMarkdown(src string) string {
	rows, merged := parseTable(src)
	if len(rows) == 0 {
		return ""
	}
	if merged {
		return cleanTableHTML(rows)
	}
	cols := 0
	for _, r := range rows {
		cols = max(cols, len(r))
	}
	var sb strings.Builder
	writeRow := func(r []cell) {
		sb.WriteString("|")
		for i := 0; i < cols; i++ {
			t := ""
			if i < len(r) {
				t = escapeCell(r[i].text)
			}
			sb.WriteString(" " + t + " |")
		}
	}
	writeRow(rows[0])
	sb.WriteString("\n|")
	for i := 0; i < cols; i++ {
		sb.WriteString("---|")
	}
	for _, r := range rows[1:] {
		sb.WriteString("\n")
		writeRow(r)
	}
	return sb.String()
}

type cell struct {
	text             string
	rowspan, colspan int
	header           bool
}

func parseTable(src string) (rows [][]cell, merged bool) {
	if strings.TrimSpace(src) == "" {
		return nil, false
	}
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return nil, false
	}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.DataAtom == atom.Tr {
			var row []cell
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type != html.ElementNode || (c.DataAtom != atom.Td && c.DataAtom != atom.Th) {
					continue
				}
				ce := cell{text: collapse(textOf(c)), rowspan: spanAttr(c, "rowspan"), colspan: spanAttr(c, "colspan"), header: c.DataAtom == atom.Th}
				if ce.rowspan > 1 || ce.colspan > 1 {
					merged = true
				}
				row = append(row, ce)
			}
			rows = append(rows, row)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return rows, merged
}

func textOf(n *html.Node) string {
	var sb strings.Builder
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
			sb.WriteString(" ")
		}
		if n.Type == html.ElementNode && n.DataAtom == atom.Br {
			sb.WriteString(" ")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(n)
	return sb.String()
}

func spanAttr(n *html.Node, key string) int {
	for _, a := range n.Attr {
		if a.Key == key {
			v := 0
			for _, r := range a.Val {
				if r < '0' || r > '9' {
					break
				}
				v = v*10 + int(r-'0')
			}
			return v
		}
	}
	return 1
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

func escapeCell(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

func cleanTableHTML(rows [][]cell) string {
	var sb strings.Builder
	sb.WriteString("<table>")
	for _, r := range rows {
		sb.WriteString("<tr>")
		for _, c := range r {
			tag := "td"
			if c.header {
				tag = "th"
			}
			sb.WriteString("<" + tag)
			if c.rowspan > 1 {
				sb.WriteString(` rowspan="` + itoa(c.rowspan) + `"`)
			}
			if c.colspan > 1 {
				sb.WriteString(` colspan="` + itoa(c.colspan) + `"`)
			}
			sb.WriteString(">" + html.EscapeString(c.text) + "</" + tag + ">")
		}
		sb.WriteString("</tr>")
	}
	sb.WriteString("</table>")
	return sb.String()
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}

// TableRows returns the cell texts of a table's HTML, row by row.
func TableRows(src string) [][]string {
	rows, _ := parseTable(src)
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		cells := make([]string, 0, len(r))
		for _, c := range r {
			cells = append(cells, c.text)
		}
		out = append(out, cells)
	}
	return out
}

// TableCells returns the flat cell texts of a table's HTML, in row order. It
// lets the text-layer merge rewrite cell text.
func TableCells(src string) []string {
	rows, _ := parseTable(src)
	var out []string
	for _, r := range rows {
		for _, c := range r {
			out = append(out, c.text)
		}
	}
	return out
}
