package simple

// vercel is Vercel Image Optimization, the Next.js grammar under
// /_vercel/image (https://vercel.com/docs/image-optimization; unpic
// providers/vercel.ts).
func vercel() *Spec {
	s := nextjs()
	s.Name = "vercel"
	s.Doc = "https://vercel.com/docs/image-optimization"
	s.Prefixes = []string{"/_vercel/image"}
	return s
}
