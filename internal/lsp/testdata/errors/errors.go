package errors

import (
	"github.com/cowpaths/golang-x-tools/internal/lsp/types"
)

func _() {
	bob.Bob() //@complete(".")
	types.b   //@complete(" //", Bob_interface)
}
