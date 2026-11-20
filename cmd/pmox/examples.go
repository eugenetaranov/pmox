package main

import (
	"strings"

	"github.com/spf13/cobra"
)

// commandExamples holds the Examples section of every command that
// doesn't set its own, by canonical path. Shortcuts show their canonical
// command's examples in their own spelling ('pmox list', not 'pmox vm
// list').
var commandExamples = map[string]string{
	"init": `  pmox init`,
	"cleanup": `  pmox cleanup                       # checklist, report, then y/N
  pmox cleanup --only snippet,log    # just these categories
  pmox cleanup --yes                 # remove without asking
  pmox cleanup --include template    # also the opt-in template category`,
	"doctor": `  pmox doctor
  pmox doctor --strict
  pmox doctor -y
  pmox doctor -o json | jq '.checks[] | select(.status=="fail")'`,
	"version": `  pmox version
  pmox version upgrade --check`,

	"vm launch": `  pmox vm launch web1
  pmox vm launch web1 --cpu 4 --mem 8 --disk 60
  pmox vm launch web1 --template ubuntu-2404-pmox-9000 --tack`,
	"vm clone": `  pmox vm clone web1 web2
  pmox vm clone web1 web2 --mem 8`,
	"vm list": `  pmox vm list
  pmox vm list --all --wide
  pmox vm list --watch
  pmox vm list -o json`,
	"vm info": `  pmox vm info web1
  pmox vm info 104 -o json`,
	"vm start": `  pmox vm start web1
  pmox vm start web1 --no-wait`,
	"vm stop": `  pmox vm stop web1
  pmox vm stop web1 web2
  pmox vm stop --hard web1`,
	"vm delete": `  pmox vm delete web1
  pmox vm delete web1 web2 --yes
  pmox vm delete --hard web1`,
	"vm shell": `  pmox vm shell web1
  pmox vm shell -A web1          # forward your ssh-agent
  pmox vm shell -L 3000 web1     # forward local port 3000`,
	"vm exec": `  pmox vm exec web1 uname -a
  pmox vm exec web1 'ls ~ | wc -l'
  pmox vm exec -u root web1 systemctl restart nginx
  pmox vm exec -- uptime          # the default or only VM`,
	"vm cp": `  pmox vm cp ./app.tar.gz web1:/tmp/
  pmox vm cp web1:/var/log/syslog ./logs/
  pmox vm cp -r ./config/ web1:/etc/app/
  pmox vm cp ./big.tar web1:/tmp/ -- -l 1000`,
	"vm sync": `  pmox vm sync ./src/ web1:/opt/app/
  pmox vm sync web1:/var/log/ ./logs/
  pmox vm sync ./src/ web1:/opt/app/ -- --delete --exclude .git`,
	"vm ssh-config": `  pmox vm ssh-config web1 >> ~/.ssh/config
  pmox vm ssh-config web1 --command
  pmox vm ssh-config web1 -o json | jq -r .hostname`,

	"template create": `  pmox template create
  pmox template create --node pve2`,
	"template list": `  pmox template list`,

	"context list":    `  pmox context list`,
	"context add":     `  pmox context add`,
	"context use":     `  pmox context use prod`,
	"context current": `  pmox context current`,
	"context edit":    `  pmox context edit prod`,
	"context rename":  `  pmox context rename 192.168.0.185 prod`,
	"context delete":  `  pmox context delete lab`,
	"config path":     `  pmox config path`,
	"config cloud-init": `  pmox config cloud-init
  pmox config cloud-init --regenerate`,

	"mount": `  pmox mount ./src web1:/opt/app
  pmox mount ./src /opt/app                  # the default or only VM
  pmox mount -F ./src web1:/opt/app          # in the foreground
  pmox mount --exclude=.git ./src web1:/opt/app -- --bwlimit=1000`,
	"mount create": `  pmox mount create ./src web1:/opt/app
  pmox mount create --no-delete --gitignore ./src web1:/opt/app`,
	"mount list": `  pmox mount list`,
	"mount delete": `  pmox mount delete
  pmox mount delete web1:/opt/app
  pmox mount delete --all web1`,

	"key list":      `  pmox key list`,
	"key show":      `  pmox key show`,
	"key unpublish": `  pmox key unpublish`,
	"access setup":  `  pmox access setup`,
	"access show": `  pmox access show
  pmox access show web1`,
	"access sync": `  pmox access sync
  pmox access sync web1`,

	"router login":      `  pmox router login`,
	"router status":     `  pmox router status`,
	"router delete":     `  pmox router delete`,
	"router route list": `  pmox router route list`,
}

// applyExamples fills in the Examples section of every command under
// root that has none, from commandExamples.
func applyExamples(root *cobra.Command) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		path := strings.TrimPrefix(c.CommandPath(), root.Name()+" ")
		canon := c.Annotations[canonicalAnnotation]
		if c.Example == "" {
			if ex, ok := commandExamples[path]; ok {
				c.Example = ex
			} else if ex, ok := commandExamples[canon]; ok && canon != "" {
				// A shortcut: its canonical command's examples, spelled short.
				c.Example = strings.ReplaceAll(ex, "pmox "+canon, "pmox "+c.Name())
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
}
