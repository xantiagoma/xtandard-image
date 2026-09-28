package ghostscript

// Geometry exposes the page geometry for tests, with c's defaults filled.
func Geometry(c *Config, p *Page) (Raster, error) {
	cc := c.withDefaults()
	return cc.geometry(p)
}

// Args exposes the Ghostscript arguments for tests.
func Args(p *Page, g Raster) []string { return args(p, g) }
