package protofiles

import (
	"bufio"
	"embed"
	"io/fs"
	"regexp"
	"strings"
	"sync"
)

//go:embed rein/v1/*.proto
var files embed.FS

var (
	loadComments sync.Once
	comments     map[string]string

	servicePattern = regexp.MustCompile(`^service\s+([A-Za-z0-9_]+)\s*\{`)
	messagePattern = regexp.MustCompile(`^(message|enum)\s+([A-Za-z0-9_]+)\s*\{`)
	rpcPattern     = regexp.MustCompile(`^rpc\s+([A-Za-z0-9_]+)\s*\(`)
	fieldPattern   = regexp.MustCompile(`^(?:repeated\s+)?(?:map<[^>]+>\s+|[A-Za-z0-9_.]+\s+)([A-Za-z0-9_]+)\s*=\s*\d+`)
)

type block struct {
	kind string
	name string
}

func Comment(fullName string) string {
	loadComments.Do(func() {
		comments = map[string]string{}
		entries, err := fs.Glob(files, "rein/v1/*.proto")
		if err != nil {
			return
		}
		for _, name := range entries {
			loadFileComments(name)
		}
	})
	return comments[fullName]
}

func loadFileComments(name string) {
	data, err := files.ReadFile(name)
	if err != nil {
		return
	}

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	var (
		stack   []block
		pending []string
	)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "//"):
			pending = append(pending, strings.TrimSpace(strings.TrimPrefix(line, "//")))
			continue
		case line == "":
			pending = nil
			continue
		}

		if matches := servicePattern.FindStringSubmatch(line); len(matches) == 2 {
			fullName := "rein.v1." + matches[1]
			storeComment(fullName, pending)
			stack = append(stack, block{kind: "service", name: fullName})
			pending = nil
			continue
		}
		if matches := messagePattern.FindStringSubmatch(line); len(matches) == 3 {
			parent := "rein.v1"
			if len(stack) > 0 && stack[len(stack)-1].kind == "message" {
				parent = stack[len(stack)-1].name
			}
			fullName := parent + "." + matches[2]
			stack = append(stack, block{kind: matches[1], name: fullName})
			pending = nil
			continue
		}
		if len(stack) > 0 && stack[len(stack)-1].kind == "service" {
			if matches := rpcPattern.FindStringSubmatch(line); len(matches) == 2 {
				storeComment(stack[len(stack)-1].name+"."+matches[1], pending)
				pending = nil
				continue
			}
		}
		if len(stack) > 0 && stack[len(stack)-1].kind == "message" {
			if matches := fieldPattern.FindStringSubmatch(line); len(matches) == 2 {
				storeComment(stack[len(stack)-1].name+"."+matches[1], pending)
			}
		}
		if strings.Contains(line, "}") {
			closeCount := strings.Count(line, "}")
			for i := 0; i < closeCount && len(stack) > 0; i++ {
				stack = stack[:len(stack)-1]
			}
		}
		pending = nil
	}
}

func storeComment(fullName string, lines []string) {
	if len(lines) == 0 {
		return
	}
	comments[fullName] = strings.TrimSpace(strings.Join(lines, " "))
}
