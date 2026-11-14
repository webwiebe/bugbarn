package mutqueue

// SetClaimRename replaces the claim rename for one test and returns the
// function that restores it.
func SetClaimRename(fn func(oldpath, newpath string) error) (restore func()) {
	prev := claimRename
	claimRename = fn
	return func() { claimRename = prev }
}
