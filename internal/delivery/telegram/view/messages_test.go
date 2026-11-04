package view

import (
	"strings"
	"testing"
)

func TestFormatAnswerHTML_UnwrappedCode(t *testing.T) {
	raw := `iota — это идентификатор в блоках const. Вот пример:

package main

import "fmt"

const (
    A = iota // 0
    B        // 1
)

func main() {
    fmt.Println(A, B)
}

В этом блоке значения констант 0 и 1.`

	formatted := formatAnswerHTML(raw)

	if !strings.Contains(formatted, `<pre><code class="language-go">`) {
		t.Fatalf("expected <pre><code class=\"language-go\"> in output, got: %s", formatted)
	}
	if !strings.Contains(formatted, `package main`) {
		t.Fatalf("expected 'package main' in output, got: %s", formatted)
	}
	if !strings.Contains(formatted, `</code></pre>`) {
		t.Fatalf("expected </code></pre> in output, got: %s", formatted)
	}
}

func TestFormatAnswerHTML_AlreadyWrappedCode(t *testing.T) {
	raw := "Текст до\n\n```go\nfunc add(a, b int) int {\n    return a + b\n}\n```\n\nТекст после с `кодом`."
	formatted := formatAnswerHTML(raw)

	if !strings.Contains(formatted, `<pre><code class="language-go">`) {
		t.Fatalf("expected pre code tag, got: %s", formatted)
	}
	if !strings.Contains(formatted, `<code>кодом</code>`) {
		t.Fatalf("expected inline code tag, got: %s", formatted)
	}
}

