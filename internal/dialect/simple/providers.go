package simple

import (
	"slices"
	"strings"
)

// Providers returns a fresh copy of every provider table, sorted by name.
// A new provider is a <name>.go file with its table function, listed here.
func Providers() []*Spec {
	specs := []*Spec{
		appwrite(),
		astro(),
		builderio(),
		cloudimage(),
		contentful(),
		contentstack(),
		directus(),
		hygraph(),
		imageengine(),
		ipx(),
		keycdn(),
		kontentai(),
		netlify(),
		nextjs(),
		scene7(),
		shopify(),
		storyblok(),
		supabase(),
		uploadcare(),
		vercel(),
		wordpress(),
	}
	slices.SortFunc(specs, func(a, b *Spec) int { return strings.Compare(a.Name, b.Name) })
	return specs
}

// Lookup returns the table of the named provider, or nil.
func Lookup(name string) *Spec {
	for _, s := range Providers() {
		if s.Name == name {
			return s
		}
	}
	return nil
}
