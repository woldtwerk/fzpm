package readme

import (
	"strings"
	"testing"
)

func TestCleanReadmeReferences(t *testing.T) {
	md := "# Title\n\n- `<nuxt-picture>` replaces `<picture>`\n\n[![npm version][npm-version-src]][npm-version-href]\n[![License][license-src]][license-href]\n\nSee the [docs][docs] and ![Shot][shot].\n\n[npm-version-src]: https://npmx.dev/api/registry/badge/version/@nuxt/image\n[npm-version-href]: https://npmx.dev/package/@nuxt/image\n[license-src]: https://img.shields.io/npm/l/x.svg\n[license-href]: https://npmjs.com\n[docs]: https://image.nuxt.com\n[shot]: ./shot.png \"Screenshot\"\n"
	out := Clean(md, "https://raw.githubusercontent.com/nuxt/image/main/")
	for _, bad := range []string{"badge", "shields", "[npm-version", "]: "} {
		if strings.Contains(out, bad) {
			t.Errorf("leftover %q in:\n%s", bad, out)
		}
	}
	for _, want := range []string{"`<nuxt-picture>` replaces `<picture>`", "[docs](https://image.nuxt.com)", "![Shot](https://raw.githubusercontent.com/nuxt/image/main/shot.png)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}
