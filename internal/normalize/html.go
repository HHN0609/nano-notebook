package normalize

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	xhtml "golang.org/x/net/html"
)

const (
	maxHTMLNodes    = 100_000
	maxHTMLDepth    = 256
	minHTMLV2Runes  = 60
	thinHTMLV2Runes = 180
)

// HTML extracts an immutable primary-content snapshot. It intentionally drops
// live behavior and navigation chrome instead of trying to preserve a web app.
func HTML(input Input) (Artifact, error) {
	input.SourceID = strings.TrimSpace(input.SourceID)
	input.ExtractionConfigID = strings.TrimSpace(input.ExtractionConfigID)
	input.Format = strings.TrimSpace(input.Format)
	if input.SourceID == "" || input.ExtractionConfigID == "" || input.Format != "html" ||
		len(input.Payload) == 0 || !utf8.Valid(input.Payload) {
		return Artifact{}, errors.New("invalid HTML normalization input")
	}
	document, err := xhtml.Parse(bytes.NewReader(input.Payload))
	if err != nil {
		return Artifact{}, fmt.Errorf("parse HTML: %w", err)
	}
	if err := validateHTMLTree(document); err != nil {
		return Artifact{}, err
	}
	if input.ExtractionConfigID == "html-primary-v2" {
		return htmlPrimaryV2(input, document)
	}
	root := firstHTMLElement(document, "main")
	if root == nil {
		root = firstHTMLElement(document, "article")
	}
	if root == nil {
		root = firstHTMLElement(document, "body")
	}
	if root == nil {
		return Artifact{}, errors.New("HTML Source has no primary document")
	}

	sourceBlocks := make([]officeBlock, 0)
	collectHTMLBlocks(root, &sourceBlocks)
	if len(sourceBlocks) == 0 {
		if fallback := htmlNodeText(root); fallback != "" {
			sourceBlocks = append(sourceBlocks, officeBlock{kind: "paragraph", text: fallback})
		}
	}
	if len(sourceBlocks) == 0 {
		return Artifact{}, errors.New("HTML Source has no usable primary content")
	}
	for index := range sourceBlocks {
		sourceBlocks[index].coordinate = SourceCoordinate{Kind: "html_block", Block: index + 1}
	}
	return finalizeOfficeArtifact(input, sourceBlocks)
}

func htmlPrimaryV2(input Input, document *xhtml.Node) (Artifact, error) {
	root := selectHTMLPrimaryV2(document)
	if root == nil {
		return Artifact{}, fmt.Errorf("%w: no primary document", ErrHTMLQuality)
	}
	text := htmlNodeTextV2(root)
	textRunes := utf8.RuneCountInString(text)
	if textRunes < minHTMLV2Runes {
		return Artifact{}, fmt.Errorf("%w: content below useful bound", ErrHTMLQuality)
	}
	if loginOrErrorPage(text) {
		return Artifact{}, fmt.Errorf("%w: access or error page", ErrHTMLQuality)
	}
	linkRunes := htmlLinkTextRunesV2(root)
	if linkRunes >= 40 && float64(linkRunes)/float64(textRunes) >= 0.80 {
		return Artifact{}, fmt.Errorf("%w: primary content is almost entirely links", ErrHTMLQuality)
	}

	blocks := make([]officeBlock, 0)
	collectHTMLBlocksV2(root, &blocks)
	if len(blocks) == 0 {
		return Artifact{}, fmt.Errorf("%w: no usable primary content", ErrHTMLQuality)
	}
	duplicateCount, duplicateRunes := duplicateHTMLBlockStats(blocks)
	allBlockRunes := 0
	for _, block := range blocks {
		allBlockRunes += utf8.RuneCountInString(block.text)
	}
	if duplicateCount*2 >= len(blocks) && duplicateRunes*2 >= allBlockRunes {
		return Artifact{}, fmt.Errorf("%w: abnormal repeated content", ErrHTMLQuality)
	}
	blocks = deduplicateHTMLBlocks(blocks)
	for index := range blocks {
		blocks[index].coordinate = SourceCoordinate{Kind: "html_block", Block: index + 1}
	}
	artifact, err := finalizeOfficeArtifact(input, blocks)
	if err != nil {
		return Artifact{}, err
	}
	if artifact.Coverage.TotalRunes < thinHTMLV2Runes {
		artifact.Coverage = Coverage{
			Status: "partial", TotalRunes: artifact.Coverage.TotalRunes,
			Gaps: []Gap{{Reason: "thin_primary_content", Impact: "non_primary", Coordinate: &SourceCoordinate{Kind: "html_block", Block: 1}}},
		}
		return Finalize(artifact)
	}
	return artifact, nil
}

func selectHTMLPrimaryV2(document *xhtml.Node) *xhtml.Node {
	for _, name := range []string{"main", "article"} {
		if node := firstVisibleHTMLElementV2(document, name); node != nil {
			return node
		}
	}
	var best *xhtml.Node
	bestScore := -1 << 30
	var visit func(*xhtml.Node)
	visit = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && excludedHTMLElementV2(node, strings.ToLower(node.Data)) {
			return
		}
		if node.Type == xhtml.ElementNode && (node.Data == "div" || node.Data == "section" || hasHTMLAttribute(node, "role", "main")) {
			textRunes := utf8.RuneCountInString(htmlNodeTextV2(node))
			if textRunes >= minHTMLV2Runes {
				linkRunes := htmlLinkTextRunesV2(node)
				score := textRunes - 2*linkRunes + primaryHintScore(node)
				if score > bestScore {
					best, bestScore = node, score
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(document)
	if best != nil {
		return best
	}
	return firstVisibleHTMLElementV2(document, "body")
}

func firstVisibleHTMLElementV2(root *xhtml.Node, name string) *xhtml.Node {
	if root.Type == xhtml.ElementNode && excludedHTMLElementV2(root, strings.ToLower(root.Data)) {
		return nil
	}
	if root.Type == xhtml.ElementNode && strings.EqualFold(root.Data, name) {
		return root
	}
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if found := firstVisibleHTMLElementV2(child, name); found != nil {
			return found
		}
	}
	return nil
}

func primaryHintScore(node *xhtml.Node) int {
	for _, attribute := range node.Attr {
		if attribute.Key != "class" && attribute.Key != "id" && attribute.Key != "role" {
			continue
		}
		value := strings.ToLower(attribute.Val)
		for _, hint := range []string{"article", "content", "entry", "main", "post", "story"} {
			if strings.Contains(value, hint) {
				return 500
			}
		}
	}
	return 0
}

func collectHTMLBlocksV2(root *xhtml.Node, blocks *[]officeBlock) {
	for node := root.FirstChild; node != nil; node = node.NextSibling {
		if node.Type != xhtml.ElementNode {
			continue
		}
		name := strings.ToLower(node.Data)
		if excludedHTMLElementV2(node, name) {
			continue
		}
		kind, level := htmlBlockKind(name)
		if kind != "" {
			text := htmlNodeTextV2(node)
			if name == "table" {
				text = htmlTableTextV2(node)
			}
			if text != "" {
				*blocks = append(*blocks, officeBlock{kind: kind, text: text, headingLevel: level})
			}
			continue
		}
		collectHTMLBlocksV2(node, blocks)
	}
}

func excludedHTMLElementV2(node *xhtml.Node, name string) bool {
	if excludedHTMLElement(node, name) {
		return true
	}
	switch name {
	case "header", "dialog":
		return true
	}
	for _, attribute := range node.Attr {
		if attribute.Key != "class" && attribute.Key != "id" && attribute.Key != "role" {
			continue
		}
		value := strings.ToLower(attribute.Val)
		for _, token := range []string{"advert", "banner", "breadcrumb", "cookie", "footer", "header", "login", "modal", "nav", "newsletter", "promo", "related", "share", "sidebar", "social"} {
			if strings.Contains(value, token) {
				return true
			}
		}
	}
	return false
}

func htmlNodeTextV2(root *xhtml.Node) string {
	values := make([]string, 0)
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && excludedHTMLElementV2(node, strings.ToLower(node.Data)) {
			return
		}
		if node.Type == xhtml.TextNode {
			values = append(values, node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return strings.Join(strings.Fields(strings.Join(values, " ")), " ")
}

func htmlTableTextV2(table *xhtml.Node) string {
	rows := make([]string, 0)
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && excludedHTMLElementV2(node, strings.ToLower(node.Data)) {
			return
		}
		if node.Type == xhtml.ElementNode && strings.EqualFold(node.Data, "tr") {
			cells := make([]string, 0)
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				if child.Type == xhtml.ElementNode && (strings.EqualFold(child.Data, "td") || strings.EqualFold(child.Data, "th")) {
					if text := htmlNodeTextV2(child); text != "" {
						cells = append(cells, text)
					}
				}
			}
			if len(cells) > 0 {
				rows = append(rows, strings.Join(cells, " | "))
			}
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(table)
	return strings.Join(rows, "\n")
}

func htmlLinkTextRunesV2(root *xhtml.Node) int {
	total := 0
	var walk func(*xhtml.Node, bool)
	walk = func(node *xhtml.Node, inLink bool) {
		if node.Type == xhtml.ElementNode && excludedHTMLElementV2(node, strings.ToLower(node.Data)) {
			return
		}
		if node.Type == xhtml.ElementNode && strings.EqualFold(node.Data, "a") {
			inLink = true
		}
		if node.Type == xhtml.TextNode && inLink {
			total += utf8.RuneCountInString(strings.Join(strings.Fields(node.Data), " "))
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, inLink)
		}
	}
	walk(root, false)
	return total
}

func hasHTMLAttribute(node *xhtml.Node, key, value string) bool {
	for _, attribute := range node.Attr {
		if strings.EqualFold(attribute.Key, key) && strings.EqualFold(strings.TrimSpace(attribute.Val), value) {
			return true
		}
	}
	return false
}

func duplicateHTMLBlockStats(blocks []officeBlock) (int, int) {
	seen := make(map[string]struct{}, len(blocks))
	count, runes := 0, 0
	for _, block := range blocks {
		key := block.kind + "\x00" + strings.ToLower(strings.Join(strings.Fields(block.text), " "))
		if _, ok := seen[key]; ok {
			count++
			runes += utf8.RuneCountInString(block.text)
			continue
		}
		seen[key] = struct{}{}
	}
	return count, runes
}

func deduplicateHTMLBlocks(blocks []officeBlock) []officeBlock {
	seen := make(map[string]struct{}, len(blocks))
	result := make([]officeBlock, 0, len(blocks))
	for _, block := range blocks {
		key := block.kind + "\x00" + strings.ToLower(strings.Join(strings.Fields(block.text), " "))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, block)
	}
	return result
}

func loginOrErrorPage(text string) bool {
	value := strings.ToLower(strings.Join(strings.Fields(text), " "))
	for _, phrase := range []string{"sign in please log in", "sign in to continue", "log in to continue", "access denied", "403 forbidden", "404 not found", "page not found", "service unavailable"} {
		if strings.Contains(value, phrase) {
			return true
		}
	}
	return false
}

func validateHTMLTree(root *xhtml.Node) error {
	type frame struct {
		node  *xhtml.Node
		depth int
	}
	count := 0
	stack := []frame{{node: root, depth: 1}}
	for len(stack) > 0 {
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		count++
		if count > maxHTMLNodes || current.depth > maxHTMLDepth {
			return fmt.Errorf("%w: HTML DOM", ErrProcessingBudget)
		}
		for child := current.node.FirstChild; child != nil; child = child.NextSibling {
			stack = append(stack, frame{node: child, depth: current.depth + 1})
		}
	}
	return nil
}

func firstHTMLElement(root *xhtml.Node, name string) *xhtml.Node {
	if root.Type == xhtml.ElementNode && strings.EqualFold(root.Data, name) {
		return root
	}
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if found := firstHTMLElement(child, name); found != nil {
			return found
		}
	}
	return nil
}

func collectHTMLBlocks(root *xhtml.Node, blocks *[]officeBlock) {
	for node := root.FirstChild; node != nil; node = node.NextSibling {
		if node.Type != xhtml.ElementNode {
			continue
		}
		name := strings.ToLower(node.Data)
		if excludedHTMLElement(node, name) {
			continue
		}
		kind, level := htmlBlockKind(name)
		if kind != "" {
			text := htmlNodeText(node)
			if name == "table" {
				text = htmlTableText(node)
			}
			if text != "" {
				*blocks = append(*blocks, officeBlock{kind: kind, text: text, headingLevel: level})
			}
			continue
		}
		collectHTMLBlocks(node, blocks)
	}
}

func excludedHTMLElement(node *xhtml.Node, name string) bool {
	switch name {
	case "script", "style", "noscript", "template", "svg", "canvas", "nav", "aside", "footer", "form", "button", "input", "select", "textarea":
		return true
	}
	for _, attribute := range node.Attr {
		if strings.EqualFold(attribute.Key, "hidden") ||
			(strings.EqualFold(attribute.Key, "aria-hidden") && strings.EqualFold(strings.TrimSpace(attribute.Val), "true")) {
			return true
		}
	}
	return false
}

func htmlBlockKind(name string) (string, int) {
	if len(name) == 2 && name[0] == 'h' && name[1] >= '1' && name[1] <= '6' {
		return "heading", int(name[1] - '0')
	}
	switch name {
	case "p", "blockquote", "figcaption":
		return "paragraph", 0
	case "li":
		return "list", 0
	case "pre":
		return "code", 0
	case "table":
		return "table", 0
	default:
		return "", 0
	}
}

func htmlNodeText(root *xhtml.Node) string {
	values := make([]string, 0)
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && excludedHTMLElement(node, strings.ToLower(node.Data)) {
			return
		}
		if node.Type == xhtml.TextNode {
			values = append(values, node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return strings.Join(strings.Fields(strings.Join(values, "")), " ")
}

func htmlTableText(table *xhtml.Node) string {
	rows := make([]string, 0)
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && strings.EqualFold(node.Data, "tr") {
			cells := make([]string, 0)
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				if child.Type == xhtml.ElementNode && (strings.EqualFold(child.Data, "td") || strings.EqualFold(child.Data, "th")) {
					if text := htmlNodeText(child); text != "" {
						cells = append(cells, text)
					}
				}
			}
			if len(cells) > 0 {
				rows = append(rows, strings.Join(cells, " | "))
			}
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(table)
	return strings.Join(rows, "\n")
}
