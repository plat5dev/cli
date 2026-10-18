package stack

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"

	"github.com/plat5dev/cli/internal/config"
)

// A sign-in plane's look. The customer plane matches the customer console (violet); the
// staff plane matches the operator console (amber), so nobody mistakes one for the other.
type plane struct {
	tag    string // beside the wordmark
	banner string // across the top: which sign-in this is, and the seeded login
	light  string // accent on paper
	dark   string // accent on ink
	staff  bool   // the banner takes the accent; the customer's is quiet
}

var (
	customerPlane = plane{
		tag:    "starter",
		banner: "local stack · customer sign-in · " + config.DevEmail + " / " + config.DevPassword,
		light:  "#6d28d9",
		dark:   "#9b7bff",
	}
	staffPlane = plane{
		tag:    "operator",
		banner: "staff sign-in · operator plane · " + config.StaffEmail + " / " + config.DevPassword,
		light:  "#b45309",
		dark:   "#f59e0b",
		staff:  true,
	}
)

const (
	paper = "#f2f2ef"
	ink   = "#0f1013"
)

// writeTheme writes an OpenAuth Theme for a local Auth: plat5.com's paper and ink, light
// or dark from the system, the wordmark with its bar in the plane's accent, and a banner.
// The wordmark and favicon are inline, so any Auth image renders them.
func writeTheme(path, title string, p plane) error {
	theme := map[string]any{
		"title":      title,
		"favicon":    svgURI(favicon(p.dark)),
		"logo":       map[string]string{"light": svgURI(wordmark(ink, p.light)), "dark": svgURI(wordmark(paper, p.dark))},
		"background": map[string]string{"light": paper, "dark": ink},
		"primary":    map[string]string{"light": p.light, "dark": p.dark},
		"font":       map[string]string{"family": `"Inter Variable", Inter, ui-sans-serif, system-ui, sans-serif`},
		"radius":     "sm",
		"css":        themeCSS(p),
	}
	data, err := json.MarshalIndent(theme, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func themeCSS(p plane) string {
	banner := `color: color-mix(in oklch, var(--color-high) 70%, transparent);
  border-bottom: 1px solid color-mix(in oklch, var(--color-high) 14%, transparent);`
	if p.staff {
		banner = `background: var(--color-primary);
  color: oklch(from var(--color-primary) clamp(0, calc((l - 0.714) * -1000), 1) 0 0);`
	}
	// Auth v0.2.2+ serves Inter at /static/inter-latin.woff2; older images fall back to the system face.
	return fmt.Sprintf(`@font-face { font-family: "Inter Variable"; src: url("/static/inter-latin.woff2") format("woff2-variations");
  font-weight: 100 900; font-display: swap; }
:root { color-scheme: light dark; }
[data-component="button"] { font-weight: 600; }
[data-component="center"] { position: relative; }
[data-component="logo"] { height: 2rem; margin-bottom: 0.5rem; transform: translateX(-2.3rem); }
[data-component="center"]::before { content: %q; position: absolute; top: 0.4rem; left: calc(50%% + 0.55rem);
  padding: 4px 6px; border: 1px solid var(--color-primary); border-radius: 4px; color: var(--color-primary);
  font: 500 11px/1 ui-monospace, SFMono-Regular, Menlo, monospace; letter-spacing: 0.02em; }
body::before { content: %q; position: fixed; z-index: 1; top: 0; left: 0; right: 0; padding: 7px 16px;
  font: 12px/1.4 ui-monospace, SFMono-Regular, Menlo, monospace; text-align: center;
  %s }`, p.tag, p.banner, banner)
}

func svgURI(svg string) string {
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg))
}

// The plat5 wordmark (web/public/brand/wordmark.svg) with its bar in the accent.
func wordmark(letters, bar string) string {
	return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 4467 1908"><path d="` + wordmarkPath +
		`" fill="` + letters + `"/><path d="M467 1708H4467V1908H467Z" fill="` + bar + `"/></svg>`
}

// The p5 monogram on ink (web/public/favicon.svg) with its bar in the accent.
func favicon(bar string) string {
	return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 2500 2500"><rect width="2500" height="2500" rx="300" fill="` + ink +
		`"/><g transform="translate(136.5 296)"><path d="` + monogramPath + `" fill="` + paper +
		`"/><path d="M467 1708H2227V1908H467Z" fill="` + bar + `"/></g></svg>`
}

const (
	wordmarkPath = "M0 1908V372H244V556H260Q280 518 317 471Q354 425 417 391Q481 358 581 358Q712 358 816 425Q920 491 982 620Q1043 748 1043 933Q1043 1116 983 1245Q923 1374 819 1442Q714 1511 581 1511Q484 1511 420 1478Q356 1445 318 1399Q281 1353 260 1314H249V1908ZM516 1304Q606 1304 666 1256Q727 1207 758 1123Q789 1038 789 932Q789 826 758 743Q727 660 667 612Q607 564 516 564Q428 564 367 610Q307 655 276 738Q244 821 244 932Q244 1043 276 1126Q308 1210 368 1257Q429 1304 516 1304ZM1441 0V1490H1192V0ZM1962 1513Q1855 1513 1770 1474Q1684 1436 1635 1360Q1586 1285 1586 1175Q1586 1081 1621 1019Q1657 958 1718 921Q1779 884 1855 865Q1930 847 2012 838Q2111 827 2172 819Q2233 811 2261 793Q2289 775 2289 737V732Q2289 647 2239 599Q2190 552 2093 552Q1993 552 1935 596Q1876 640 1855 698L1622 659Q1653 557 1721 491Q1789 424 1884 391Q1979 358 2092 358Q2168 358 2246 376Q2325 394 2391 437Q2458 480 2499 555Q2540 630 2540 744V1490H2298V1336H2290Q2267 1381 2224 1422Q2181 1462 2116 1488Q2051 1513 1962 1513ZM2023 1325Q2106 1325 2166 1292Q2226 1260 2258 1206Q2291 1152 2291 1089V953Q2278 964 2248 973Q2218 982 2179 989Q2141 996 2104 1001Q2067 1006 2041 1009Q1981 1017 1932 1036Q1884 1054 1856 1087Q1828 1120 1828 1174Q1828 1249 1883 1287Q1938 1325 2023 1325ZM3284 372V568H2652V372ZM2811 106H3060V1156Q3060 1226 3090 1258Q3120 1291 3185 1291Q3202 1291 3223 1288Q3243 1286 3262 1282L3305 1480Q3266 1492 3224 1499Q3182 1505 3141 1505Q2988 1505 2900 1423Q2811 1341 2811 1200ZM3939 1510Q3796 1510 3683 1456Q3570 1402 3503 1307Q3437 1212 3433 1090H3681Q3688 1182 3762 1241Q3835 1300 3939 1300Q4021 1300 4083 1263Q4146 1226 4182 1160Q4218 1094 4218 1008Q4218 922 4181 855Q4144 787 4079 749Q4015 711 3931 711Q3863 711 3798 735Q3733 760 3693 800L3460 763L3539 0H4391V216H3752L3709 618H3717Q3759 570 3834 540Q3908 509 3997 509Q4133 509 4239 573Q4345 637 4406 748Q4467 859 4467 1003Q4467 1150 4400 1265Q4332 1379 4213 1445Q4094 1510 3939 1510Z"
	monogramPath = "M0 1908V372H244V556H260Q280 518 317 471Q354 425 417 391Q481 358 581 358Q712 358 816 425Q920 491 982 620Q1043 748 1043 933Q1043 1116 983 1245Q923 1374 819 1442Q714 1511 581 1511Q484 1511 420 1478Q356 1445 318 1399Q281 1353 260 1314H249V1908ZM516 1304Q606 1304 666 1256Q727 1207 758 1123Q789 1038 789 932Q789 826 758 743Q727 660 667 612Q607 564 516 564Q428 564 367 610Q307 655 276 738Q244 821 244 932Q244 1043 276 1126Q308 1210 368 1257Q429 1304 516 1304ZM1699 1510Q1556 1510 1443 1456Q1330 1402 1263 1307Q1197 1212 1193 1090H1441Q1448 1182 1522 1241Q1595 1300 1699 1300Q1781 1300 1843 1263Q1906 1226 1942 1160Q1978 1094 1978 1008Q1978 922 1941 855Q1904 787 1839 749Q1775 711 1691 711Q1623 711 1558 735Q1493 760 1453 800L1220 763L1299 0H2151V216H1512L1469 618H1477Q1519 570 1594 540Q1668 509 1757 509Q1893 509 1999 573Q2105 637 2166 748Q2227 859 2227 1003Q2227 1150 2160 1265Q2092 1379 1973 1445Q1854 1510 1699 1510Z"
)
