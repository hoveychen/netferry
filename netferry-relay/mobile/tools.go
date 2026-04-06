//go:build tools

// This file ensures gomobile/gobind dependencies stay in go.sum.
// gobind imports these packages at code-generation time, but nothing in our
// source code imports them directly, so go mod tidy would remove them.
package mobile

import (
	_ "golang.org/x/mobile/bind"
	_ "golang.org/x/mobile/bind/java"
	_ "golang.org/x/mobile/bind/objc"
	_ "golang.org/x/mobile/bind/seq"
)
