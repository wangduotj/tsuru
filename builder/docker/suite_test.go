// Copyright 2012 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package docker

import (
	"math/rand"
	"testing"

	dtesting "github.com/fsouza/go-dockerclient/testing"
	"github.com/tsuru/config"
	"github.com/tsuru/tsuru/app"
	"github.com/tsuru/tsuru/auth"
	"github.com/tsuru/tsuru/auth/native"
	"github.com/tsuru/tsuru/db"
	"github.com/tsuru/tsuru/db/dbtest"
	"github.com/tsuru/tsuru/provision"
	"github.com/tsuru/tsuru/provision/provisiontest"
	"github.com/tsuru/tsuru/quota"
	"github.com/tsuru/tsuru/router/routertest"
	"github.com/tsuru/tsuru/storage"
	_ "github.com/tsuru/tsuru/storage/mongodb"
	authTypes "github.com/tsuru/tsuru/types/auth"
	"golang.org/x/crypto/bcrypt"
	"gopkg.in/check.v1"
)

type S struct {
	b           *dockerBuilder
	conn        *db.Storage
	user        *auth.User
	team        *authTypes.Team
	token       auth.Token
	provisioner *provisiontest.FakeProvisioner
	server      *dtesting.DockerServer
}

var _ = check.Suite(&S{})

func Test(t *testing.T) {
	check.TestingT(t)
}

func (s *S) SetUpSuite(c *check.C) {
	config.Set("log:disable-syslog", true)
	config.Set("auth:hash-cost", bcrypt.MinCost)
	config.Set("database:url", "127.0.0.1:27017")
	config.Set("database:name", "builder_docker_tests_s")
	config.Set("routers:fake:type", "fake")
	config.Set("routers:fake:default", true)
	var err error
	s.conn, err = db.Conn()
	c.Assert(err, check.IsNil)
	err = dbtest.ClearAllCollections(s.conn.Apps().Database)
	c.Assert(err, check.IsNil)
	s.provisioner = provisiontest.ProvisionerInstance
	provision.DefaultProvisioner = "fake"
}

func (s *S) TearDownSuite(c *check.C) {
	s.conn.Apps().Database.DropDatabase()
	s.conn.Close()
}

func (s *S) SetUpTest(c *check.C) {
	routertest.FakeRouter.Reset()
	s.provisioner.Reset()
	rand.Seed(0)
	err := dbtest.ClearAllCollections(s.conn.Apps().Database)
	c.Assert(err, check.IsNil)
	err = provision.AddPool(provision.AddPoolOptions{
		Name:        "thepool",
		Default:     true,
		Provisioner: "fake",
	})
	c.Assert(err, check.IsNil)
	p := app.Plan{
		Name:     "default",
		Default:  true,
		CpuShare: 100,
	}
	err = p.Save()
	c.Assert(err, check.IsNil)
	s.b = &dockerBuilder{}
	s.server, err = dtesting.NewServer("127.0.0.1:0", nil, nil)
	c.Assert(err, check.IsNil)
	s.user = &auth.User{Email: "whiskeyjack@genabackis.com", Password: "123456", Quota: quota.Unlimited}
	nativeScheme := auth.ManagedScheme(native.NativeScheme{})
	app.AuthScheme = nativeScheme
	_, err = nativeScheme.Create(s.user)
	c.Assert(err, check.IsNil)
	s.team = &authTypes.Team{Name: "admin"}
	err = storage.TeamRepository.Insert(*s.team)
	c.Assert(err, check.IsNil)
	s.token, err = nativeScheme.Login(map[string]string{"email": s.user.Email, "password": "123456"})
	c.Assert(err, check.IsNil)
}

func (s *S) TearDownTest(c *check.C) {
	s.server.Stop()
}
