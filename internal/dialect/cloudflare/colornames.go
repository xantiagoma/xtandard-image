package cloudflare

// namedColor returns the sRGB value of a CSS Color Module Level 4 named
// colour (lower-case name; the list matches weserv's parsers/color.cpp,
// BSD-3-Clause, plus the "grey" spellings).
//
//nolint:gocyclo,funlen // a flat lookup table
func namedColor(name string) (uint32, bool) {
	switch name {
	case "aliceblue":
		return 0xf0f8ff, true
	case "antiquewhite":
		return 0xfaebd7, true
	case "aqua":
		return 0x00ffff, true
	case "aquamarine":
		return 0x7fffd4, true
	case "azure":
		return 0xf0ffff, true
	case "beige":
		return 0xf5f5dc, true
	case "bisque":
		return 0xffe4c4, true
	case "black":
		return 0x000000, true
	case "blanchedalmond":
		return 0xffebcd, true
	case "blue":
		return 0x0000ff, true
	case "blueviolet":
		return 0x8a2be2, true
	case "brown":
		return 0xa52a2a, true
	case "burlywood":
		return 0xdeb887, true
	case "cadetblue":
		return 0x5f9ea0, true
	case "chartreuse":
		return 0x7fff00, true
	case "chocolate":
		return 0xd2691e, true
	case "coral":
		return 0xff7f50, true
	case "cornflowerblue":
		return 0x6495ed, true
	case "cornsilk":
		return 0xfff8dc, true
	case "crimson":
		return 0xdc143c, true
	case "cyan":
		return 0x00ffff, true
	case "darkblue":
		return 0x00008b, true
	case "darkcyan":
		return 0x008b8b, true
	case "darkgoldenrod":
		return 0xb8860b, true
	case "darkgray", "darkgrey":
		return 0xa9a9a9, true
	case "darkgreen":
		return 0x006400, true
	case "darkkhaki":
		return 0xbdb76b, true
	case "darkmagenta":
		return 0x8b008b, true
	case "darkolivegreen":
		return 0x556b2f, true
	case "darkorange":
		return 0xff8c00, true
	case "darkorchid":
		return 0x9932cc, true
	case "darkred":
		return 0x8b0000, true
	case "darksalmon":
		return 0xe9967a, true
	case "darkseagreen":
		return 0x8fbc8f, true
	case "darkslateblue":
		return 0x483d8b, true
	case "darkslategray", "darkslategrey":
		return 0x2f4f4f, true
	case "darkturquoise":
		return 0x00ced1, true
	case "darkviolet":
		return 0x9400d3, true
	case "deeppink":
		return 0xff1493, true
	case "deepskyblue":
		return 0x00bfff, true
	case "dimgray", "dimgrey":
		return 0x696969, true
	case "dodgerblue":
		return 0x1e90ff, true
	case "firebrick":
		return 0xb22222, true
	case "floralwhite":
		return 0xfffaf0, true
	case "forestgreen":
		return 0x228b22, true
	case "fuchsia":
		return 0xff00ff, true
	case "gainsboro":
		return 0xdcdcdc, true
	case "ghostwhite":
		return 0xf8f8ff, true
	case "gold":
		return 0xffd700, true
	case "goldenrod":
		return 0xdaa520, true
	case "gray", "grey":
		return 0x808080, true
	case "green":
		return 0x008000, true
	case "greenyellow":
		return 0xadff2f, true
	case "honeydew":
		return 0xf0fff0, true
	case "hotpink":
		return 0xff69b4, true
	case "indianred":
		return 0xcd5c5c, true
	case "indigo":
		return 0x4b0082, true
	case "ivory":
		return 0xfffff0, true
	case "khaki":
		return 0xf0e68c, true
	case "lavender":
		return 0xe6e6fa, true
	case "lavenderblush":
		return 0xfff0f5, true
	case "lawngreen":
		return 0x7cfc00, true
	case "lemonchiffon":
		return 0xfffacd, true
	case "lightblue":
		return 0xadd8e6, true
	case "lightcoral":
		return 0xf08080, true
	case "lightcyan":
		return 0xe0ffff, true
	case "lightgoldenrodyellow":
		return 0xfafad2, true
	case "lightgray", "lightgrey":
		return 0xd3d3d3, true
	case "lightgreen":
		return 0x90ee90, true
	case "lightpink":
		return 0xffb6c1, true
	case "lightsalmon":
		return 0xffa07a, true
	case "lightseagreen":
		return 0x20b2aa, true
	case "lightskyblue":
		return 0x87cefa, true
	case "lightslategray", "lightslategrey":
		return 0x778899, true
	case "lightsteelblue":
		return 0xb0c4de, true
	case "lightyellow":
		return 0xffffe0, true
	case "lime":
		return 0x00ff00, true
	case "limegreen":
		return 0x32cd32, true
	case "linen":
		return 0xfaf0e6, true
	case "magenta":
		return 0xff00ff, true
	case "maroon":
		return 0x800000, true
	case "mediumaquamarine":
		return 0x66cdaa, true
	case "mediumblue":
		return 0x0000cd, true
	case "mediumorchid":
		return 0xba55d3, true
	case "mediumpurple":
		return 0x9370db, true
	case "mediumseagreen":
		return 0x3cb371, true
	case "mediumslateblue":
		return 0x7b68ee, true
	case "mediumspringgreen":
		return 0x00fa9a, true
	case "mediumturquoise":
		return 0x48d1cc, true
	case "mediumvioletred":
		return 0xc71585, true
	case "midnightblue":
		return 0x191970, true
	case "mintcream":
		return 0xf5fffa, true
	case "mistyrose":
		return 0xffe4e1, true
	case "moccasin":
		return 0xffe4b5, true
	case "navajowhite":
		return 0xffdead, true
	case "navy":
		return 0x000080, true
	case "oldlace":
		return 0xfdf5e6, true
	case "olive":
		return 0x808000, true
	case "olivedrab":
		return 0x6b8e23, true
	case "orange":
		return 0xffa500, true
	case "orangered":
		return 0xff4500, true
	case "orchid":
		return 0xda70d6, true
	case "palegoldenrod":
		return 0xeee8aa, true
	case "palegreen":
		return 0x98fb98, true
	case "paleturquoise":
		return 0xafeeee, true
	case "palevioletred":
		return 0xdb7093, true
	case "papayawhip":
		return 0xffefd5, true
	case "peachpuff":
		return 0xffdab9, true
	case "peru":
		return 0xcd853f, true
	case "pink":
		return 0xffc0cb, true
	case "plum":
		return 0xdda0dd, true
	case "powderblue":
		return 0xb0e0e6, true
	case "purple":
		return 0x800080, true
	case "rebeccapurple":
		return 0x663399, true
	case "red":
		return 0xff0000, true
	case "rosybrown":
		return 0xbc8f8f, true
	case "royalblue":
		return 0x4169e1, true
	case "saddlebrown":
		return 0x8b4513, true
	case "salmon":
		return 0xfa8072, true
	case "sandybrown":
		return 0xf4a460, true
	case "seagreen":
		return 0x2e8b57, true
	case "seashell":
		return 0xfff5ee, true
	case "sienna":
		return 0xa0522d, true
	case "silver":
		return 0xc0c0c0, true
	case "skyblue":
		return 0x87ceeb, true
	case "slateblue":
		return 0x6a5acd, true
	case "slategray", "slategrey":
		return 0x708090, true
	case "snow":
		return 0xfffafa, true
	case "springgreen":
		return 0x00ff7f, true
	case "steelblue":
		return 0x4682b4, true
	case "tan":
		return 0xd2b48c, true
	case "teal":
		return 0x008080, true
	case "thistle":
		return 0xd8bfd8, true
	case "tomato":
		return 0xff6347, true
	case "turquoise":
		return 0x40e0d0, true
	case "violet":
		return 0xee82ee, true
	case "wheat":
		return 0xf5deb3, true
	case "white":
		return 0xffffff, true
	case "whitesmoke":
		return 0xf5f5f5, true
	case "yellow":
		return 0xffff00, true
	case "yellowgreen":
		return 0x9acd32, true
	}
	return 0, false
}
