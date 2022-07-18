package c

import "github.com/cowpaths/golang-x-tools/internal/lsp/rename/b"

func _() {
	b.Hello() //@rename("Hello", "Goodbye")
}
