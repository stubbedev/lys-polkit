package locale

import (
	"embed"
	"fmt"
	"io/fs"

	"github.com/stubbedev/lys-lib/i18n"
)

type Message string

const (
	Action               Message = "Action"
	Vendor               Message = "Vendor"
	Website              Message = "Website"
	Program              Message = "Program"
	CommandLine          Message = "Command line"
	Caller               Message = "Caller"
	AuthenticationFailed Message = "Authentication failed"
)

var Messages = [...]Message{Action, Vendor, Website, Program, CommandLine, Caller, AuthenticationFailed}

const Domain = "lys-polkit"

//go:embed */LC_MESSAGES/*.po
var Embedded embed.FS

type Catalog struct{ c *i18n.Catalog }

func Load(fsys fs.FS) (Catalog, error) {
	c, err := i18n.Load(fsys, Domain, i18n.Locales())
	if err != nil {
		return Catalog{}, fmt.Errorf("load the %s message catalog: %w", Domain, err)
	}
	return Catalog{c: c}, nil
}

func (c Catalog) Get(m Message) string { return c.c.Get(string(m)) }
