// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package cloud_test

import (
	"io/ioutil"
	"strings"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"github.com/juju/juju/cmd/juju/cloud"
	"github.com/juju/juju/juju/osenv"
	"github.com/juju/juju/testing"
)

type listSuite struct{}

var _ = gc.Suite(&listSuite{})

func (s *listSuite) TestListPublic(c *gc.C) {
	defer osenv.SetJujuXDGDataHome(osenv.SetJujuXDGDataHome(c.MkDir()))
	ctx, err := testing.RunCommand(c, cloud.NewListCloudsCommand())
	c.Assert(err, jc.ErrorIsNil)
	out := testing.Stdout(ctx)
	out = strings.Replace(out, "\n", "", -1)
	// Just check a snippet of the output to make sure it looks ok.
	c.Assert(out, gc.Matches, `.*aws-china[ ]*ec2[ ]*cn-north-1.*`)
}

func (s *listSuite) TestListPublicAndPersonal(c *gc.C) {
	jujuXDGDataHome := c.MkDir()
	defer osenv.SetJujuXDGDataHome(osenv.SetJujuXDGDataHome(jujuXDGDataHome))
	data := `
clouds:
  homestack:
    type: openstack
    auth-types: [userpass, access-key]
    endpoint: http://homestack
    regions:
      london:
        endpoint: http://london/1.0
`[1:]
	err := ioutil.WriteFile(osenv.JujuXDGDataHomePath("clouds.yaml"), []byte(data), 0600)
	c.Assert(err, jc.ErrorIsNil)

	ctx, err := testing.RunCommand(c, cloud.NewListCloudsCommand())
	c.Assert(err, jc.ErrorIsNil)
	out := testing.Stdout(ctx)
	out = strings.Replace(out, "\n", "", -1)
	// Just check a snippet of the output to make sure it looks ok.
	// local: clouds are last.
	c.Assert(out, gc.Matches, `.*local\:homestack[ ]*openstack[ ]*london$`)
}

func (s *listSuite) TestListYAML(c *gc.C) {
	defer osenv.SetJujuXDGDataHome(osenv.SetJujuXDGDataHome(c.MkDir()))
	ctx, err := testing.RunCommand(c, cloud.NewListCloudsCommand(), "--format", "yaml")
	c.Assert(err, jc.ErrorIsNil)
	out := testing.Stdout(ctx)
	out = strings.Replace(out, "\n", "", -1)
	// Just check a snippet of the output to make sure it looks ok.
	c.Assert(out, gc.Matches, `.*aws:[ ]*defined: public[ ]*type: ec2[ ]*auth-types: \[access-key\].*`)
}

func (s *listSuite) TestListJSON(c *gc.C) {
	defer osenv.SetJujuXDGDataHome(osenv.SetJujuXDGDataHome(c.MkDir()))
	ctx, err := testing.RunCommand(c, cloud.NewListCloudsCommand(), "--format", "json")
	c.Assert(err, jc.ErrorIsNil)
	out := testing.Stdout(ctx)
	out = strings.Replace(out, "\n", "", -1)
	// Just check a snippet of the output to make sure it looks ok.
	c.Assert(out, gc.Matches, `.*{"aws":{"defined":"public","type":"ec2","auth-types":\["access-key"\].*`)
}