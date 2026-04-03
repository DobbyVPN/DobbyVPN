//go:build android

package protected_dialer

var MakeSocketProtected func(fd uintptr)

type androidProtector struct{}

func (a *androidProtector) Protect(fd uintptr, network string) {
	if MakeSocketProtected != nil {
		MakeSocketProtected(fd)
	}
}

func init() {
	protector = &androidProtector{}
}
