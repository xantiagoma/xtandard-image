package filters

// colorName returns the RGB value of a lower-case CSS3 colour keyword: the
// 148 names of Pillow's ImageColor.colormap, which thumbor's PIL engine
// accepts in fill() and background_color().
func colorName(s string) (r, g, b uint8, ok bool) {
	switch s {
	case "aliceblue":
		return 240, 248, 255, true
	case "antiquewhite":
		return 250, 235, 215, true
	case "aqua":
		return 0, 255, 255, true
	case "aquamarine":
		return 127, 255, 212, true
	case "azure":
		return 240, 255, 255, true
	case "beige":
		return 245, 245, 220, true
	case "bisque":
		return 255, 228, 196, true
	case "black":
		return 0, 0, 0, true
	case "blanchedalmond":
		return 255, 235, 205, true
	case "blue":
		return 0, 0, 255, true
	case "blueviolet":
		return 138, 43, 226, true
	case "brown":
		return 165, 42, 42, true
	case "burlywood":
		return 222, 184, 135, true
	case "cadetblue":
		return 95, 158, 160, true
	case "chartreuse":
		return 127, 255, 0, true
	case "chocolate":
		return 210, 105, 30, true
	case "coral":
		return 255, 127, 80, true
	case "cornflowerblue":
		return 100, 149, 237, true
	case "cornsilk":
		return 255, 248, 220, true
	case "crimson":
		return 220, 20, 60, true
	case "cyan":
		return 0, 255, 255, true
	case "darkblue":
		return 0, 0, 139, true
	case "darkcyan":
		return 0, 139, 139, true
	case "darkgoldenrod":
		return 184, 134, 11, true
	case "darkgray":
		return 169, 169, 169, true
	case "darkgreen":
		return 0, 100, 0, true
	case "darkgrey":
		return 169, 169, 169, true
	case "darkkhaki":
		return 189, 183, 107, true
	case "darkmagenta":
		return 139, 0, 139, true
	case "darkolivegreen":
		return 85, 107, 47, true
	case "darkorange":
		return 255, 140, 0, true
	case "darkorchid":
		return 153, 50, 204, true
	case "darkred":
		return 139, 0, 0, true
	case "darksalmon":
		return 233, 150, 122, true
	case "darkseagreen":
		return 143, 188, 143, true
	case "darkslateblue":
		return 72, 61, 139, true
	case "darkslategray":
		return 47, 79, 79, true
	case "darkslategrey":
		return 47, 79, 79, true
	case "darkturquoise":
		return 0, 206, 209, true
	case "darkviolet":
		return 148, 0, 211, true
	case "deeppink":
		return 255, 20, 147, true
	case "deepskyblue":
		return 0, 191, 255, true
	case "dimgray":
		return 105, 105, 105, true
	case "dimgrey":
		return 105, 105, 105, true
	case "dodgerblue":
		return 30, 144, 255, true
	case "firebrick":
		return 178, 34, 34, true
	case "floralwhite":
		return 255, 250, 240, true
	case "forestgreen":
		return 34, 139, 34, true
	case "fuchsia":
		return 255, 0, 255, true
	case "gainsboro":
		return 220, 220, 220, true
	case "ghostwhite":
		return 248, 248, 255, true
	case "gold":
		return 255, 215, 0, true
	case "goldenrod":
		return 218, 165, 32, true
	case "gray":
		return 128, 128, 128, true
	case "green":
		return 0, 128, 0, true
	case "greenyellow":
		return 173, 255, 47, true
	case "grey":
		return 128, 128, 128, true
	case "honeydew":
		return 240, 255, 240, true
	case "hotpink":
		return 255, 105, 180, true
	case "indianred":
		return 205, 92, 92, true
	case "indigo":
		return 75, 0, 130, true
	case "ivory":
		return 255, 255, 240, true
	case "khaki":
		return 240, 230, 140, true
	case "lavender":
		return 230, 230, 250, true
	case "lavenderblush":
		return 255, 240, 245, true
	case "lawngreen":
		return 124, 252, 0, true
	case "lemonchiffon":
		return 255, 250, 205, true
	case "lightblue":
		return 173, 216, 230, true
	case "lightcoral":
		return 240, 128, 128, true
	case "lightcyan":
		return 224, 255, 255, true
	case "lightgoldenrodyellow":
		return 250, 250, 210, true
	case "lightgray":
		return 211, 211, 211, true
	case "lightgreen":
		return 144, 238, 144, true
	case "lightgrey":
		return 211, 211, 211, true
	case "lightpink":
		return 255, 182, 193, true
	case "lightsalmon":
		return 255, 160, 122, true
	case "lightseagreen":
		return 32, 178, 170, true
	case "lightskyblue":
		return 135, 206, 250, true
	case "lightslategray":
		return 119, 136, 153, true
	case "lightslategrey":
		return 119, 136, 153, true
	case "lightsteelblue":
		return 176, 196, 222, true
	case "lightyellow":
		return 255, 255, 224, true
	case "lime":
		return 0, 255, 0, true
	case "limegreen":
		return 50, 205, 50, true
	case "linen":
		return 250, 240, 230, true
	case "magenta":
		return 255, 0, 255, true
	case "maroon":
		return 128, 0, 0, true
	case "mediumaquamarine":
		return 102, 205, 170, true
	case "mediumblue":
		return 0, 0, 205, true
	case "mediumorchid":
		return 186, 85, 211, true
	case "mediumpurple":
		return 147, 112, 219, true
	case "mediumseagreen":
		return 60, 179, 113, true
	case "mediumslateblue":
		return 123, 104, 238, true
	case "mediumspringgreen":
		return 0, 250, 154, true
	case "mediumturquoise":
		return 72, 209, 204, true
	case "mediumvioletred":
		return 199, 21, 133, true
	case "midnightblue":
		return 25, 25, 112, true
	case "mintcream":
		return 245, 255, 250, true
	case "mistyrose":
		return 255, 228, 225, true
	case "moccasin":
		return 255, 228, 181, true
	case "navajowhite":
		return 255, 222, 173, true
	case "navy":
		return 0, 0, 128, true
	case "oldlace":
		return 253, 245, 230, true
	case "olive":
		return 128, 128, 0, true
	case "olivedrab":
		return 107, 142, 35, true
	case "orange":
		return 255, 165, 0, true
	case "orangered":
		return 255, 69, 0, true
	case "orchid":
		return 218, 112, 214, true
	case "palegoldenrod":
		return 238, 232, 170, true
	case "palegreen":
		return 152, 251, 152, true
	case "paleturquoise":
		return 175, 238, 238, true
	case "palevioletred":
		return 219, 112, 147, true
	case "papayawhip":
		return 255, 239, 213, true
	case "peachpuff":
		return 255, 218, 185, true
	case "peru":
		return 205, 133, 63, true
	case "pink":
		return 255, 192, 203, true
	case "plum":
		return 221, 160, 221, true
	case "powderblue":
		return 176, 224, 230, true
	case "purple":
		return 128, 0, 128, true
	case "rebeccapurple":
		return 102, 51, 153, true
	case "red":
		return 255, 0, 0, true
	case "rosybrown":
		return 188, 143, 143, true
	case "royalblue":
		return 65, 105, 225, true
	case "saddlebrown":
		return 139, 69, 19, true
	case "salmon":
		return 250, 128, 114, true
	case "sandybrown":
		return 244, 164, 96, true
	case "seagreen":
		return 46, 139, 87, true
	case "seashell":
		return 255, 245, 238, true
	case "sienna":
		return 160, 82, 45, true
	case "silver":
		return 192, 192, 192, true
	case "skyblue":
		return 135, 206, 235, true
	case "slateblue":
		return 106, 90, 205, true
	case "slategray":
		return 112, 128, 144, true
	case "slategrey":
		return 112, 128, 144, true
	case "snow":
		return 255, 250, 250, true
	case "springgreen":
		return 0, 255, 127, true
	case "steelblue":
		return 70, 130, 180, true
	case "tan":
		return 210, 180, 140, true
	case "teal":
		return 0, 128, 128, true
	case "thistle":
		return 216, 191, 216, true
	case "tomato":
		return 255, 99, 71, true
	case "turquoise":
		return 64, 224, 208, true
	case "violet":
		return 238, 130, 238, true
	case "wheat":
		return 245, 222, 179, true
	case "white":
		return 255, 255, 255, true
	case "whitesmoke":
		return 245, 245, 245, true
	case "yellow":
		return 255, 255, 0, true
	case "yellowgreen":
		return 154, 205, 50, true
	}
	return 0, 0, 0, false
}
