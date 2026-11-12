package initialize

import "github.com/cultofthecode/orbit/internal/sandbox/internal/hostcheck"

// errUnresolvableLink is the error of a dangling symlink whose target has
// ".." after a name, which the handler reports from hostcheck.
var errUnresolvableLink = hostcheck.ErrUnresolvableLink
