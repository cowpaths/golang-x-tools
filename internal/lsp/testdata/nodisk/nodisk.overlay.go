package nodisk

import (
	"github.com/cowpaths/golang-x-tools/internal/lsp/foo"
)

func _() {
	foo.Foo() //@complete("F", Foo, IntFoo, StructFoo)
}
