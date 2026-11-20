// Package adminui contains the embedded management console page.
package adminui

import _ "embed"

// Page is the self-contained admin HTML document.
//
//go:embed admin.html
var Page []byte
