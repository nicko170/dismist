package options

import (
	"sync"

	"github.com/nicko170/dismist/internal/options/firmware"
)

// Declarations returns every declared option, unvalidated.
func Declarations() []Option {
	var all []Option
	for _, group := range [][]Option{wlanOptions, siteOptions, apOptions, rfOptions} {
		all = append(all, group...)
	}
	return all
}

// PathExists reports whether a firmware path is in ap33-0.15.paths.
func PathExists(path string) bool {
	_, ok := firmware.AP33().Lookup(path)
	return ok
}

var (
	defaultOnce     sync.Once
	defaultRegistry *Registry
	defaultErr      error
)

// Default returns the validated registry of every declared option. It
// panics if the declarations are invalid; registry_test.go catches that in
// CI first.
func Default() *Registry {
	defaultOnce.Do(func() { defaultRegistry, defaultErr = New(Declarations(), PathExists) })
	if defaultErr != nil {
		panic("options: invalid registry: " + defaultErr.Error())
	}
	return defaultRegistry
}
