package protected_dialer

type platformProtector interface {
	Protect(fd uintptr, network string) error
}

var protector platformProtector
