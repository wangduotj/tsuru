// Copyright 2012 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/tsuru/config"
	"github.com/tsuru/tsuru/action"
	"github.com/tsuru/tsuru/app/bind"
	"github.com/tsuru/tsuru/auth"
	"github.com/tsuru/tsuru/db"
	"github.com/tsuru/tsuru/db/dbtest"
	"github.com/tsuru/tsuru/provision/provisiontest"
	"github.com/tsuru/tsuru/storage"
	_ "github.com/tsuru/tsuru/storage/mongodb"
	authTypes "github.com/tsuru/tsuru/types/auth"
	"gopkg.in/check.v1"
	"gopkg.in/mgo.v2/bson"
)

type InstanceSuite struct {
	conn *db.Storage
	team *authTypes.Team
	user *auth.User
}

var _ = check.Suite(&InstanceSuite{})

func (s *InstanceSuite) SetUpSuite(c *check.C) {
	var err error
	config.Set("log:disable-syslog", true)
	config.Set("database:url", "127.0.0.1:27017")
	config.Set("database:name", "tsuru_service_instance_test")
	s.conn, err = db.Conn()
	c.Assert(err, check.IsNil)
}

func (s *InstanceSuite) SetUpTest(c *check.C) {
	dbtest.ClearAllCollections(s.conn.Apps().Database)
	s.user = &auth.User{Email: "cidade@raul.com", Password: "123"}
	s.team = &authTypes.Team{Name: "Raul"}
	s.conn.Users().Insert(s.user)
	storage.TeamRepository.Insert(*s.team)
}

func (s *InstanceSuite) TearDownSuite(c *check.C) {
	s.conn.ServiceInstances().Database.DropDatabase()
	s.conn.Close()
}

func (s *InstanceSuite) TestDeleteServiceInstance(c *check.C) {
	si := &ServiceInstance{Name: "MySQL"}
	s.conn.ServiceInstances().Insert(&si)
	DeleteInstance(si, "")
	query := bson.M{"name": si.Name}
	qtd, err := s.conn.ServiceInstances().Find(query).Count()
	c.Assert(err, check.IsNil)
	c.Assert(qtd, check.Equals, 0)
}

func (s *InstanceSuite) TestRetrieveAssociatedService(c *check.C) {
	service := Service{Name: "my_service"}
	s.conn.Services().Insert(&service)
	serviceInstance := &ServiceInstance{
		Name:        service.Name,
		ServiceName: service.Name,
	}
	rService := serviceInstance.Service()
	c.Assert(service.Name, check.Equals, rService.Name)
}

func (s *InstanceSuite) TestFindApp(c *check.C) {
	instance := ServiceInstance{
		Name: "myinstance",
		Apps: []string{"app1", "app2"},
	}
	c.Assert(instance.FindApp("app1"), check.Equals, 0)
	c.Assert(instance.FindApp("app2"), check.Equals, 1)
	c.Assert(instance.FindApp("what"), check.Equals, -1)
}

func (s *InstanceSuite) TestBindApp(c *check.C) {
	oldBindAppDBAction := bindAppDBAction
	oldBindAppEndpointAction := bindAppEndpointAction
	oldSetBoundEnvsAction := setBoundEnvsAction
	oldBindUnitsAction := bindUnitsAction
	defer func() {
		bindAppDBAction = oldBindAppDBAction
		bindAppEndpointAction = oldBindAppEndpointAction
		setBoundEnvsAction = oldSetBoundEnvsAction
		bindUnitsAction = oldBindUnitsAction
	}()
	var calls []string
	var params []interface{}
	bindAppDBAction = &action.Action{
		Forward: func(ctx action.FWContext) (action.Result, error) {
			calls = append(calls, "bindAppDBAction")
			params = ctx.Params
			return nil, nil
		},
	}
	bindAppEndpointAction = &action.Action{
		Forward: func(ctx action.FWContext) (action.Result, error) {
			calls = append(calls, "bindAppEndpointAction")
			return nil, nil
		},
	}
	setBoundEnvsAction = &action.Action{
		Forward: func(ctx action.FWContext) (action.Result, error) {
			calls = append(calls, "setBoundEnvsAction")
			return nil, nil
		},
	}
	bindUnitsAction = &action.Action{
		Forward: func(ctx action.FWContext) (action.Result, error) {
			calls = append(calls, "bindUnitsAction")
			return nil, nil
		},
	}
	var si ServiceInstance
	a := provisiontest.NewFakeApp("myapp", "python", 1)
	var buf bytes.Buffer
	err := si.BindApp(a, true, &buf)
	c.Assert(err, check.IsNil)
	expectedCalls := []string{
		"bindAppDBAction", "bindAppEndpointAction",
		"setBoundEnvsAction", "bindUnitsAction",
	}
	expectedParams := []interface{}{&bindPipelineArgs{app: a, serviceInstance: &si, writer: &buf, shouldRestart: true}}
	c.Assert(calls, check.DeepEquals, expectedCalls)
	c.Assert(params, check.DeepEquals, expectedParams)
	c.Assert(buf.String(), check.Equals, "")
}

func (s *InstanceSuite) TestGetServiceInstancesBoundToApp(c *check.C) {
	srvc := Service{Name: "mysql"}
	err := s.conn.Services().Insert(&srvc)
	c.Assert(err, check.IsNil)
	sInstance := ServiceInstance{
		Name:        "t3sql",
		ServiceName: "mysql",
		Tags:        []string{},
		Teams:       []string{s.team.Name},
		Apps:        []string{"app1", "app2"},
		BoundUnits:  []Unit{},
	}
	err = s.conn.ServiceInstances().Insert(&sInstance)
	c.Assert(err, check.IsNil)
	sInstance2 := ServiceInstance{
		Name:        "s9sql",
		ServiceName: "mysql",
		Tags:        []string{},
		Apps:        []string{"app1"},
		BoundUnits:  []Unit{},
		Teams:       []string{},
	}
	err = s.conn.ServiceInstances().Insert(&sInstance2)
	c.Assert(err, check.IsNil)
	sInstances, err := GetServiceInstancesBoundToApp("app2")
	c.Assert(err, check.IsNil)
	expected := []ServiceInstance{sInstance}
	c.Assert(sInstances, check.DeepEquals, expected)
	sInstances, err = GetServiceInstancesBoundToApp("app1")
	c.Assert(err, check.IsNil)
	expected = []ServiceInstance{sInstance, sInstance2}
	c.Assert(sInstances, check.DeepEquals, expected)
}

func (s *InstanceSuite) TestGetServiceInstancesByServices(c *check.C) {
	srvc := Service{Name: "mysql"}
	err := s.conn.Services().Insert(&srvc)
	c.Assert(err, check.IsNil)
	sInstance := ServiceInstance{Name: "t3sql", ServiceName: "mysql", Teams: []string{s.team.Name}}
	err = s.conn.ServiceInstances().Insert(&sInstance)
	c.Assert(err, check.IsNil)
	sInstance2 := ServiceInstance{Name: "s9sql", ServiceName: "mysql", Tags: []string{}}
	err = s.conn.ServiceInstances().Insert(&sInstance2)
	c.Assert(err, check.IsNil)
	sInstances, err := GetServiceInstancesByServices([]Service{srvc})
	c.Assert(err, check.IsNil)
	expected := []ServiceInstance{{Name: "t3sql", ServiceName: "mysql", Tags: []string{}}, sInstance2}
	c.Assert(sInstances, check.DeepEquals, expected)
}

func (s *InstanceSuite) TestGetServiceInstancesByServicesWithoutAnyExistingServiceInstances(c *check.C) {
	srvc := Service{Name: "mysql"}
	err := s.conn.Services().Insert(&srvc)
	c.Assert(err, check.IsNil)
	sInstances, err := GetServiceInstancesByServices([]Service{srvc})
	c.Assert(err, check.IsNil)
	c.Assert(sInstances, check.DeepEquals, []ServiceInstance(nil))
}

func (s *InstanceSuite) TestGetServiceInstancesByServicesWithTwoServices(c *check.C) {
	srvc := Service{Name: "mysql"}
	err := s.conn.Services().Insert(&srvc)
	c.Assert(err, check.IsNil)
	srvc2 := Service{Name: "mongodb"}
	err = s.conn.Services().Insert(&srvc2)
	c.Assert(err, check.IsNil)
	sInstance := ServiceInstance{Name: "t3sql", ServiceName: "mysql", Teams: []string{s.team.Name}}
	err = s.conn.ServiceInstances().Insert(&sInstance)
	c.Assert(err, check.IsNil)
	sInstance2 := ServiceInstance{Name: "s9nosql", ServiceName: "mongodb", Tags: []string{"tag 1", "tag 2"}}
	err = s.conn.ServiceInstances().Insert(&sInstance2)
	c.Assert(err, check.IsNil)
	sInstances, err := GetServiceInstancesByServices([]Service{srvc, srvc2})
	c.Assert(err, check.IsNil)
	expected := []ServiceInstance{{Name: "t3sql", ServiceName: "mysql", Tags: []string{}}, sInstance2}
	c.Assert(sInstances, check.DeepEquals, expected)
}

func (s *InstanceSuite) TestGenericServiceInstancesFilter(c *check.C) {
	srvc := Service{Name: "mysql"}
	teams := []string{s.team.Name}
	query := genericServiceInstancesFilter(srvc, teams)
	c.Assert(query, check.DeepEquals, bson.M{"service_name": srvc.Name, "teams": bson.M{"$in": teams}})
}

func (s *InstanceSuite) TestGenericServiceInstancesFilterWithServiceSlice(c *check.C) {
	services := []Service{
		{Name: "mysql"},
		{Name: "mongodb"},
	}
	names := []string{"mysql", "mongodb"}
	teams := []string{s.team.Name}
	query := genericServiceInstancesFilter(services, teams)
	c.Assert(query, check.DeepEquals, bson.M{"service_name": bson.M{"$in": names}, "teams": bson.M{"$in": teams}})
}

func (s *InstanceSuite) TestGenericServiceInstancesFilterWithoutSpecifingTeams(c *check.C) {
	services := []Service{
		{Name: "mysql"},
		{Name: "mongodb"},
	}
	names := []string{"mysql", "mongodb"}
	teams := []string{}
	query := genericServiceInstancesFilter(services, teams)
	c.Assert(query, check.DeepEquals, bson.M{"service_name": bson.M{"$in": names}})
}

func (s *InstanceSuite) TestAdditionalInfo(c *check.C) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"label": "key", "value": "value"}, {"label": "key2", "value": "value2"}]`))
	}))
	defer ts.Close()
	srvc := Service{Name: "mysql", Endpoint: map[string]string{"production": ts.URL}, Password: "s3cr3t"}
	err := s.conn.Services().Insert(&srvc)
	c.Assert(err, check.IsNil)
	si := ServiceInstance{Name: "ql", ServiceName: srvc.Name}
	info, err := si.Info("")
	c.Assert(err, check.IsNil)
	expected := map[string]string{
		"key":  "value",
		"key2": "value2",
	}
	c.Assert(info, check.DeepEquals, expected)
}

func (s *InstanceSuite) TestMarshalJSON(c *check.C) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"label": "key", "value": "value"}]`))
	}))
	defer ts.Close()
	srvc := Service{Name: "mysql", Endpoint: map[string]string{"production": ts.URL}, Password: "s3cr3t"}
	err := s.conn.Services().Insert(&srvc)
	c.Assert(err, check.IsNil)
	si := ServiceInstance{Name: "ql", ServiceName: srvc.Name}
	data, err := json.Marshal(&si)
	c.Assert(err, check.IsNil)
	var result map[string]interface{}
	err = json.Unmarshal(data, &result)
	c.Assert(err, check.IsNil)
	expected := map[string]interface{}{
		"Id":          float64(0),
		"Name":        "ql",
		"PlanName":    "",
		"Teams":       nil,
		"Apps":        nil,
		"ServiceName": "mysql",
		"Info":        map[string]interface{}{"key": "value"},
		"TeamOwner":   "",
	}
	c.Assert(result, check.DeepEquals, expected)
}

func (s *InstanceSuite) TestMarshalJSONWithoutInfo(c *check.C) {
	srvc := Service{Name: "mysql", Endpoint: map[string]string{"production": ""}}
	err := s.conn.Services().Insert(&srvc)
	c.Assert(err, check.IsNil)
	si := ServiceInstance{Name: "ql", ServiceName: srvc.Name}
	data, err := json.Marshal(&si)
	c.Assert(err, check.IsNil)
	var result map[string]interface{}
	err = json.Unmarshal(data, &result)
	c.Assert(err, check.IsNil)
	expected := map[string]interface{}{
		"Id":          float64(0),
		"Name":        "ql",
		"PlanName":    "",
		"Teams":       nil,
		"Apps":        nil,
		"ServiceName": "mysql",
		"Info":        nil,
		"TeamOwner":   "",
	}
	c.Assert(result, check.DeepEquals, expected)
}

func (s *InstanceSuite) TestMarshalJSONWithoutEndpoint(c *check.C) {
	srvc := Service{Name: "mysql"}
	err := s.conn.Services().Insert(&srvc)
	c.Assert(err, check.IsNil)
	si := ServiceInstance{Name: "ql", ServiceName: srvc.Name}
	data, err := json.Marshal(&si)
	c.Assert(err, check.IsNil)
	var result map[string]interface{}
	err = json.Unmarshal(data, &result)
	c.Assert(err, check.IsNil)
	expected := map[string]interface{}{
		"Id":          float64(0),
		"Name":        "ql",
		"PlanName":    "",
		"Teams":       nil,
		"Apps":        nil,
		"ServiceName": "mysql",
		"Info":        nil,
		"TeamOwner":   "",
	}
	c.Assert(result, check.DeepEquals, expected)
}

func (s *InstanceSuite) TestDeleteInstance(c *check.C) {
	h := TestHandler{}
	ts := httptest.NewServer(&h)
	defer ts.Close()
	srv := Service{Name: "mongodb", Endpoint: map[string]string{"production": ts.URL}, Password: "s3cr3t"}
	err := s.conn.Services().Insert(&srv)
	c.Assert(err, check.IsNil)
	si := ServiceInstance{Name: "instance", ServiceName: srv.Name}
	err = s.conn.ServiceInstances().Insert(&si)
	c.Assert(err, check.IsNil)
	err = DeleteInstance(&si, "")
	h.Lock()
	defer h.Unlock()
	c.Assert(err, check.IsNil)
	l, err := s.conn.ServiceInstances().Find(bson.M{"name": si.Name}).Count()
	c.Assert(err, check.IsNil)
	c.Assert(l, check.Equals, 0)
	c.Assert(h.url, check.Equals, "/resources/"+si.Name)
	c.Assert(h.method, check.Equals, "DELETE")
}

func (s *InstanceSuite) TestDeleteInstanceWithApps(c *check.C) {
	si := ServiceInstance{Name: "instance", Apps: []string{"foo"}}
	err := s.conn.ServiceInstances().Insert(&si)
	c.Assert(err, check.IsNil)
	s.conn.ServiceInstances().Remove(bson.M{"name": si.Name})
	err = DeleteInstance(&si, "")
	c.Assert(err, check.ErrorMatches, "^This service instance is bound to at least one app. Unbind them before removing it$")
}

func (s *InstanceSuite) TestCreateServiceInstance(c *check.C) {
	var requests int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		atomic.AddInt32(&requests, 1)
	}))
	defer ts.Close()
	srv := Service{Name: "mongodb", Endpoint: map[string]string{"production": ts.URL}, Password: "s3cr3t"}
	err := s.conn.Services().Insert(&srv)
	c.Assert(err, check.IsNil)
	instance := ServiceInstance{Name: "instance", PlanName: "small", TeamOwner: s.team.Name, Tags: []string{"tag1", "tag2"}}
	err = CreateServiceInstance(instance, &srv, s.user, "")
	c.Assert(err, check.IsNil)
	si, err := GetServiceInstance("mongodb", "instance")
	c.Assert(err, check.IsNil)
	c.Assert(atomic.LoadInt32(&requests), check.Equals, int32(1))
	c.Assert(si.PlanName, check.Equals, "small")
	c.Assert(si.TeamOwner, check.Equals, s.team.Name)
	c.Assert(si.Teams, check.DeepEquals, []string{s.team.Name})
	c.Assert(si.Tags, check.DeepEquals, []string{"tag1", "tag2"})
}

func (s *InstanceSuite) TestCreateServiceInstanceWithSameInstanceName(c *check.C) {
	var requests int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		atomic.AddInt32(&requests, 1)
	}))
	defer ts.Close()
	srv := []Service{
		{Name: "mongodb", Endpoint: map[string]string{"production": ts.URL}, Password: "s3cr3t"},
		{Name: "mongodb2", Endpoint: map[string]string{"production": ts.URL}, Password: "s3cr3t"},
		{Name: "mongodb3", Endpoint: map[string]string{"production": ts.URL}, Password: "s3cr3t"},
	}
	instance := ServiceInstance{Name: "instance", PlanName: "small", TeamOwner: s.team.Name}
	for _, service := range srv {
		err := s.conn.Services().Insert(&service)
		c.Assert(err, check.IsNil)
		err = CreateServiceInstance(instance, &service, s.user, "")
		c.Assert(err, check.IsNil)
	}
	si, err := GetServiceInstance("mongodb3", "instance")
	c.Assert(err, check.IsNil)
	c.Assert(atomic.LoadInt32(&requests), check.Equals, int32(3))
	c.Assert(si.PlanName, check.Equals, "small")
	c.Assert(si.TeamOwner, check.Equals, s.team.Name)
	c.Assert(si.Teams, check.DeepEquals, []string{s.team.Name})
	c.Assert(si.Name, check.Equals, "instance")
	c.Assert(si.ServiceName, check.Equals, "mongodb3")
	err = CreateServiceInstance(instance, &srv[0], s.user, "")
	c.Assert(err, check.Equals, ErrInstanceNameAlreadyExists)
}

func (s *InstanceSuite) TestCreateSpecifyOwner(c *check.C) {
	var requests int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		atomic.AddInt32(&requests, 1)
	}))
	defer ts.Close()
	team := authTypes.Team{Name: "owner"}
	err := storage.TeamRepository.Insert(team)
	c.Assert(err, check.IsNil)
	srv := Service{Name: "mongodb", Endpoint: map[string]string{"production": ts.URL}, Password: "s3cr3t"}
	err = s.conn.Services().Insert(&srv)
	c.Assert(err, check.IsNil)
	instance := ServiceInstance{Name: "instance", PlanName: "small", TeamOwner: team.Name}
	err = CreateServiceInstance(instance, &srv, s.user, "")
	c.Assert(err, check.IsNil)
	si, err := GetServiceInstance("mongodb", "instance")
	c.Assert(err, check.IsNil)
	c.Assert(atomic.LoadInt32(&requests), check.Equals, int32(1))
	c.Assert(si.TeamOwner, check.Equals, team.Name)
}

func (s *InstanceSuite) TestCreateServiceInstanceNoTeamOwner(c *check.C) {
	var requests int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		atomic.AddInt32(&requests, 1)
	}))
	defer ts.Close()
	team := authTypes.Team{Name: "owner"}
	err := storage.TeamRepository.Insert(team)
	c.Assert(err, check.IsNil)
	srv := Service{Name: "mongodb", Endpoint: map[string]string{"production": ts.URL}, Password: "s3cr3t"}
	err = s.conn.Services().Insert(&srv)
	c.Assert(err, check.IsNil)
	instance := ServiceInstance{Name: "instance", PlanName: "small"}
	err = CreateServiceInstance(instance, &srv, s.user, "")
	c.Assert(err, check.Equals, ErrTeamMandatory)
}

func (s *InstanceSuite) TestCreateServiceInstanceNameShouldBeUnique(c *check.C) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()
	srv := Service{Name: "mongodb", Endpoint: map[string]string{"production": ts.URL}, Password: "s3cr3t"}
	err := s.conn.Services().Insert(&srv)
	c.Assert(err, check.IsNil)
	instance := ServiceInstance{Name: "instance", TeamOwner: s.team.Name}
	err = CreateServiceInstance(instance, &srv, s.user, "")
	c.Assert(err, check.IsNil)
	err = CreateServiceInstance(instance, &srv, s.user, "")
	c.Assert(err, check.Equals, ErrInstanceNameAlreadyExists)
}

func (s *InstanceSuite) TestCreateServiceInstanceEndpointFailure(c *check.C) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	srv := Service{Name: "mongodb", Endpoint: map[string]string{"production": ts.URL}, Password: "s3cr3t"}
	err := s.conn.Services().Insert(&srv)
	c.Assert(err, check.IsNil)
	instance := ServiceInstance{Name: "instance"}
	err = CreateServiceInstance(instance, &srv, s.user, "")
	c.Assert(err, check.NotNil)
	count, err := s.conn.ServiceInstances().Find(bson.M{"name": "instance"}).Count()
	c.Assert(err, check.IsNil)
	c.Assert(count, check.Equals, 0)
}

func (s *InstanceSuite) TestCreateServiceInstanceValidatesTheName(c *check.C) {
	var tests = []struct {
		input string
		err   error
	}{
		{"my-service", nil},
		{"my_service", ErrInvalidInstanceName},
		{"MyService", ErrInvalidInstanceName},
		{"a1", nil},
		{"--app", ErrInvalidInstanceName},
		{"123servico", ErrInvalidInstanceName},
		{"a", nil},
		{"a@123", ErrInvalidInstanceName},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()
	srv := Service{Name: "mongodb", Endpoint: map[string]string{"production": ts.URL}, Password: "s3cr3t"}
	err := s.conn.Services().Insert(&srv)
	c.Assert(err, check.IsNil)
	for _, t := range tests {
		instance := ServiceInstance{Name: t.input, TeamOwner: s.team.Name}
		err := CreateServiceInstance(instance, &srv, s.user, "")
		c.Check(err, check.Equals, t.err)
	}
}

func (s *InstanceSuite) TestCreateServiceInstanceRemovesDuplicatedAndEmptyTags(c *check.C) {
	var requests int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		atomic.AddInt32(&requests, 1)
	}))
	defer ts.Close()
	srv := Service{Name: "mongodb", Endpoint: map[string]string{"production": ts.URL}, Password: "s3cr3t"}
	err := s.conn.Services().Insert(&srv)
	c.Assert(err, check.IsNil)
	instance := ServiceInstance{Name: "instance", PlanName: "small", TeamOwner: s.team.Name, Tags: []string{"", "  tag1 ", "tag1", "  "}}
	err = CreateServiceInstance(instance, &srv, s.user, "")
	c.Assert(err, check.IsNil)
	si, err := GetServiceInstance("mongodb", "instance")
	c.Assert(err, check.IsNil)
	c.Assert(atomic.LoadInt32(&requests), check.Equals, int32(1))
	c.Assert(si.Tags, check.DeepEquals, []string{"tag1"})
}

func (s *InstanceSuite) TestUpdateServiceInstance(c *check.C) {
	var requests int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		atomic.AddInt32(&requests, 1)
	}))
	defer ts.Close()
	srv := Service{Name: "mongodb", Endpoint: map[string]string{"production": ts.URL}, Password: "s3cr3t"}
	err := s.conn.Services().Insert(&srv)
	c.Assert(err, check.IsNil)
	instance := ServiceInstance{Name: "instance", ServiceName: "mongodb", PlanName: "small", TeamOwner: s.team.Name, Tags: []string{"tag1"}}
	err = CreateServiceInstance(instance, &srv, s.user, "")
	c.Assert(err, check.IsNil)
	instance.Description = "desc"
	instance.Tags = []string{"tag2"}
	err = instance.Update(instance)
	c.Assert(err, check.IsNil)
	var si ServiceInstance
	err = s.conn.ServiceInstances().Find(bson.M{"name": "instance"}).One(&si)
	c.Assert(err, check.IsNil)
	c.Assert(si.PlanName, check.Equals, "small")
	c.Assert(si.TeamOwner, check.Equals, s.team.Name)
	c.Assert(si.Description, check.Equals, "desc")
	c.Assert(si.Tags, check.DeepEquals, []string{"tag2"})
}

func (s *InstanceSuite) TestUpdateServiceInstanceRemovesDuplicatedAndEmptyTags(c *check.C) {
	var requests int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		atomic.AddInt32(&requests, 1)
	}))
	defer ts.Close()
	srv := Service{Name: "mongodb", Endpoint: map[string]string{"production": ts.URL}, Password: "s3cr3t"}
	err := s.conn.Services().Insert(&srv)
	c.Assert(err, check.IsNil)
	instance := ServiceInstance{Name: "instance", ServiceName: "mongodb", PlanName: "small", TeamOwner: s.team.Name, Tags: []string{"tag1"}}
	err = CreateServiceInstance(instance, &srv, s.user, "")
	c.Assert(err, check.IsNil)
	instance.Tags = []string{"tag2", " ", " tag2 "}
	err = instance.Update(instance)
	c.Assert(err, check.IsNil)
	var si ServiceInstance
	err = s.conn.ServiceInstances().Find(bson.M{"name": "instance"}).One(&si)
	c.Assert(err, check.IsNil)
	c.Assert(si.Tags, check.DeepEquals, []string{"tag2"})
}

func (s *InstanceSuite) TestStatus(c *check.C) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()
	srv := Service{Name: "mongodb", Endpoint: map[string]string{"production": ts.URL}}
	err := s.conn.Services().Insert(&srv)
	c.Assert(err, check.IsNil)
	si := ServiceInstance{Name: "instance", ServiceName: srv.Name}
	status, err := si.Status("")
	c.Assert(err, check.IsNil)
	c.Assert(status, check.Equals, "up")
}

func (s *InstanceSuite) TestGetServiceInstance(c *check.C) {
	s.conn.ServiceInstances().Insert(
		ServiceInstance{Name: "mongo-1", ServiceName: "mongodb", Teams: []string{s.team.Name}},
		ServiceInstance{Name: "mongo-2", ServiceName: "mongodb", Teams: []string{s.team.Name}},
		ServiceInstance{Name: "mongo-3", ServiceName: "mongodb", Teams: []string{s.team.Name}},
		ServiceInstance{Name: "mongo-4", ServiceName: "mongodb", Teams: []string{s.team.Name}},
		ServiceInstance{Name: "mongo-5", ServiceName: "mongodb"},
	)
	instance, err := GetServiceInstance("mongodb", "mongo-1")
	c.Assert(err, check.IsNil)
	c.Assert(instance.Name, check.Equals, "mongo-1")
	c.Assert(instance.ServiceName, check.Equals, "mongodb")
	c.Assert(instance.Teams, check.DeepEquals, []string{s.team.Name})
	instance, err = GetServiceInstance("mongodb", "mongo-6")
	c.Assert(instance, check.IsNil)
	c.Assert(err, check.Equals, ErrServiceInstanceNotFound)
	instance, err = GetServiceInstance("mongodb", "mongo-5")
	c.Assert(err, check.IsNil)
	c.Assert(instance.Name, check.Equals, "mongo-5")
}

func (s *InstanceSuite) TestGetIdentfier(c *check.C) {
	srv := ServiceInstance{Name: "mongodb"}
	identifier := srv.GetIdentifier()
	c.Assert(identifier, check.Equals, srv.Name)
	srv.Id = 10
	identifier = srv.GetIdentifier()
	c.Assert(identifier, check.Equals, strconv.Itoa(srv.Id))
}

func (s *InstanceSuite) TestGrantTeamToInstance(c *check.C) {
	user := &auth.User{Email: "test@raul.com", Password: "123"}
	team := authTypes.Team{Name: "test2"}
	s.conn.Users().Insert(user)
	storage.TeamRepository.Insert(team)
	srvc := Service{Name: "mysql", Teams: []string{team.Name}, IsRestricted: false}
	err := s.conn.Services().Insert(&srvc)
	c.Assert(err, check.IsNil)
	sInstance := ServiceInstance{
		Name:        "j4sql",
		ServiceName: srvc.Name,
	}
	err = s.conn.ServiceInstances().Insert(&sInstance)
	c.Assert(err, check.IsNil)
	sInstance.Grant(team.Name)
	si, err := GetServiceInstance("mysql", "j4sql")
	c.Assert(err, check.IsNil)
	c.Assert(si.Teams, check.DeepEquals, []string{"test2"})
}

func (s *InstanceSuite) TestRevokeTeamToInstance(c *check.C) {
	user := &auth.User{Email: "test@raul.com", Password: "123"}
	team := authTypes.Team{Name: "test2"}
	s.conn.Users().Insert(user)
	storage.TeamRepository.Insert(team)
	srvc := Service{Name: "mysql", Teams: []string{team.Name}, IsRestricted: false}
	err := s.conn.Services().Insert(&srvc)
	c.Assert(err, check.IsNil)
	sInstance := ServiceInstance{
		Name:        "j4sql",
		ServiceName: srvc.Name,
		Teams:       []string{team.Name},
	}
	err = s.conn.ServiceInstances().Insert(&sInstance)
	c.Assert(err, check.IsNil)
	si, err := GetServiceInstance("mysql", "j4sql")
	c.Assert(err, check.IsNil)
	c.Assert(si.Teams, check.DeepEquals, []string{"test2"})
	sInstance.Revoke(team.Name)
	si, err = GetServiceInstance("mysql", "j4sql")
	c.Assert(err, check.IsNil)
	c.Assert(si.Teams, check.DeepEquals, []string{})
}

func (s *InstanceSuite) TestUnbindApp(c *check.C) {
	var reqs []*http.Request
	var mut sync.Mutex
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mut.Lock()
		defer mut.Unlock()
		reqs = append(reqs, r)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	serv := Service{Name: "mysql", Endpoint: map[string]string{"production": ts.URL}, Password: "s3cr3t"}
	err := serv.Create()
	c.Assert(err, check.IsNil)
	a := provisiontest.NewFakeApp("myapp", "static", 2)
	si := ServiceInstance{
		Name:        "my-mysql",
		ServiceName: "mysql",
		Teams:       []string{s.team.Name},
		Apps:        []string{a.GetName()},
	}
	err = si.Create()
	c.Assert(err, check.IsNil)
	err = a.AddInstance(bind.AddInstanceArgs{
		Envs: []bind.ServiceEnvVar{
			{EnvVar: bind.EnvVar{Name: "ENV1", Value: "VAL1"}, ServiceName: "mysql", InstanceName: "my-mysql"},
			{EnvVar: bind.EnvVar{Name: "ENV2", Value: "VAL2"}, ServiceName: "mysql", InstanceName: "my-mysql"},
		},
		ShouldRestart: true,
	})
	c.Assert(err, check.IsNil)
	units, err := a.Units()
	c.Assert(err, check.IsNil)
	for i := range units {
		err = si.BindUnit(a, &units[i])
		c.Assert(err, check.IsNil)
	}
	var buf bytes.Buffer
	err = si.UnbindApp(a, false, &buf)
	c.Assert(err, check.IsNil)
	c.Assert(buf.String(), check.Matches, "remove instance")
	c.Assert(reqs, check.HasLen, 5)
	c.Assert(reqs[0].Method, check.Equals, "POST")
	c.Assert(reqs[0].URL.Path, check.Equals, "/resources/my-mysql/bind")
	c.Assert(reqs[1].Method, check.Equals, "POST")
	c.Assert(reqs[1].URL.Path, check.Equals, "/resources/my-mysql/bind")
	c.Assert(reqs[2].Method, check.Equals, "DELETE")
	c.Assert(reqs[2].URL.Path, check.Equals, "/resources/my-mysql/bind")
	c.Assert(reqs[3].Method, check.Equals, "DELETE")
	c.Assert(reqs[3].URL.Path, check.Equals, "/resources/my-mysql/bind")
	c.Assert(reqs[4].Method, check.Equals, "DELETE")
	c.Assert(reqs[4].URL.Path, check.Equals, "/resources/my-mysql/bind-app")
	siDB, err := GetServiceInstance("mysql", si.Name)
	c.Assert(err, check.IsNil)
	c.Assert(siDB.Apps, check.DeepEquals, []string{})
	c.Assert(a.GetServiceEnvs(), check.DeepEquals, []bind.ServiceEnvVar{})
}

func (s *InstanceSuite) TestUnbindAppFailureInUnbindAppCall(c *check.C) {
	var reqs []*http.Request
	var mut sync.Mutex
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mut.Lock()
		defer mut.Unlock()
		reqs = append(reqs, r)
		if r.Method == "DELETE" && r.URL.Path == "/resources/my-mysql/bind-app" {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("my unbind app err"))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	serv := Service{Name: "mysql", Endpoint: map[string]string{"production": ts.URL}, Password: "s3cr3t"}
	err := serv.Create()
	c.Assert(err, check.IsNil)
	a := provisiontest.NewFakeApp("myapp", "static", 2)
	si := ServiceInstance{
		Name:        "my-mysql",
		ServiceName: "mysql",
		Teams:       []string{s.team.Name},
		Apps:        []string{a.GetName()},
	}
	err = si.Create()
	c.Assert(err, check.IsNil)
	err = a.AddInstance(bind.AddInstanceArgs{
		Envs: []bind.ServiceEnvVar{
			{EnvVar: bind.EnvVar{Name: "ENV1", Value: "VAL1"}, ServiceName: "mysql", InstanceName: "my-mysql"},
			{EnvVar: bind.EnvVar{Name: "ENV2", Value: "VAL2"}, ServiceName: "mysql", InstanceName: "my-mysql"},
		},
		ShouldRestart: true,
	})
	c.Assert(err, check.IsNil)
	units, err := a.Units()
	c.Assert(err, check.IsNil)
	for i := range units {
		err = si.BindUnit(a, &units[i])
		c.Assert(err, check.IsNil)
	}
	var buf bytes.Buffer
	err = si.UnbindApp(a, true, &buf)
	c.Assert(err, check.ErrorMatches, `Failed to unbind \("/resources/my-mysql/bind-app"\): invalid response: my unbind app err \(code: 500\)`)
	c.Assert(buf.String(), check.Matches, "")
	c.Assert(si.Apps, check.DeepEquals, []string{"myapp"})
	c.Assert(reqs, check.HasLen, 7)
	c.Assert(reqs[0].Method, check.Equals, "POST")
	c.Assert(reqs[0].URL.Path, check.Equals, "/resources/my-mysql/bind")
	c.Assert(reqs[1].Method, check.Equals, "POST")
	c.Assert(reqs[1].URL.Path, check.Equals, "/resources/my-mysql/bind")
	c.Assert(reqs[2].Method, check.Equals, "DELETE")
	c.Assert(reqs[2].URL.Path, check.Equals, "/resources/my-mysql/bind")
	c.Assert(reqs[3].Method, check.Equals, "DELETE")
	c.Assert(reqs[3].URL.Path, check.Equals, "/resources/my-mysql/bind")
	c.Assert(reqs[4].Method, check.Equals, "DELETE")
	c.Assert(reqs[4].URL.Path, check.Equals, "/resources/my-mysql/bind-app")
	c.Assert(reqs[5].Method, check.Equals, "POST")
	c.Assert(reqs[5].URL.Path, check.Equals, "/resources/my-mysql/bind")
	c.Assert(reqs[6].Method, check.Equals, "POST")
	c.Assert(reqs[6].URL.Path, check.Equals, "/resources/my-mysql/bind")
	siDB, err := GetServiceInstance(si.ServiceName, si.Name)
	c.Assert(err, check.IsNil)
	c.Assert(siDB.Apps, check.DeepEquals, []string{"myapp"})
	c.Assert(a.GetServiceEnvs(), check.DeepEquals, []bind.ServiceEnvVar{
		{EnvVar: bind.EnvVar{Name: "ENV1", Value: "VAL1"}, ServiceName: "mysql", InstanceName: "my-mysql"},
		{EnvVar: bind.EnvVar{Name: "ENV2", Value: "VAL2"}, ServiceName: "mysql", InstanceName: "my-mysql"},
	})
}

func (s *InstanceSuite) TestUnbindAppFailureInAppEnvSet(c *check.C) {
	var reqs []*http.Request
	var mut sync.Mutex
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mut.Lock()
		defer mut.Unlock()
		reqs = append(reqs, r)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	serv := Service{Name: "mysql", Endpoint: map[string]string{"production": ts.URL}, Password: "s3cr3t"}
	err := serv.Create()
	c.Assert(err, check.IsNil)
	a := provisiontest.NewFakeApp("myapp", "static", 2)
	si := ServiceInstance{
		Name:        "my-mysql",
		ServiceName: "mysql",
		Teams:       []string{s.team.Name},
		Apps:        []string{a.GetName()},
	}
	err = si.Create()
	c.Assert(err, check.IsNil)
	units, err := a.Units()
	c.Assert(err, check.IsNil)
	for i := range units {
		err = si.BindUnit(a, &units[i])
		c.Assert(err, check.IsNil)
	}
	var buf bytes.Buffer
	err = si.UnbindApp(a, true, &buf)
	c.Assert(err, check.ErrorMatches, `instance not found`)
	c.Assert(buf.String(), check.Matches, "")
	c.Assert(si.Apps, check.DeepEquals, []string{"myapp"})
	c.Assert(reqs, check.HasLen, 8)
	c.Assert(reqs[0].Method, check.Equals, "POST")
	c.Assert(reqs[0].URL.Path, check.Equals, "/resources/my-mysql/bind")
	c.Assert(reqs[1].Method, check.Equals, "POST")
	c.Assert(reqs[1].URL.Path, check.Equals, "/resources/my-mysql/bind")
	c.Assert(reqs[2].Method, check.Equals, "DELETE")
	c.Assert(reqs[2].URL.Path, check.Equals, "/resources/my-mysql/bind")
	c.Assert(reqs[3].Method, check.Equals, "DELETE")
	c.Assert(reqs[3].URL.Path, check.Equals, "/resources/my-mysql/bind")
	c.Assert(reqs[4].Method, check.Equals, "DELETE")
	c.Assert(reqs[4].URL.Path, check.Equals, "/resources/my-mysql/bind-app")
	c.Assert(reqs[5].Method, check.Equals, "POST")
	c.Assert(reqs[5].URL.Path, check.Equals, "/resources/my-mysql/bind-app")
	c.Assert(reqs[6].Method, check.Equals, "POST")
	c.Assert(reqs[6].URL.Path, check.Equals, "/resources/my-mysql/bind")
	c.Assert(reqs[7].Method, check.Equals, "POST")
	c.Assert(reqs[7].URL.Path, check.Equals, "/resources/my-mysql/bind")
	siDB, err := GetServiceInstance(si.ServiceName, si.Name)
	c.Assert(err, check.IsNil)
	c.Assert(siDB.Apps, check.DeepEquals, []string{"myapp"})
}

func (s *InstanceSuite) TestBindAppFullPipeline(c *check.C) {
	var reqs []*http.Request
	var mut sync.Mutex
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mut.Lock()
		defer mut.Unlock()
		reqs = append(reqs, r)
		w.WriteHeader(http.StatusOK)
		if r.URL.Path == "/resources/my-mysql/bind-app" && r.Method == "POST" {
			w.Write([]byte(`{"ENV1": "VAL1", "ENV2": "VAL2"}`))
		}
	}))
	defer ts.Close()
	serv := Service{Name: "mysql", Endpoint: map[string]string{"production": ts.URL}, Password: "s3cr3t"}
	err := serv.Create()
	c.Assert(err, check.IsNil)
	si := ServiceInstance{
		Name:        "my-mysql",
		ServiceName: "mysql",
		Teams:       []string{s.team.Name},
	}
	err = si.Create()
	c.Assert(err, check.IsNil)
	a := provisiontest.NewFakeApp("myapp", "static", 2)
	var buf bytes.Buffer
	err = si.BindApp(a, true, &buf)
	c.Assert(err, check.IsNil)
	c.Assert(buf.String(), check.Matches, "add instance")
	c.Assert(reqs, check.HasLen, 3)
	c.Assert(reqs[0].Method, check.Equals, "POST")
	c.Assert(reqs[0].URL.Path, check.Equals, "/resources/my-mysql/bind-app")
	c.Assert(reqs[1].Method, check.Equals, "POST")
	c.Assert(reqs[1].URL.Path, check.Equals, "/resources/my-mysql/bind")
	c.Assert(reqs[2].Method, check.Equals, "POST")
	c.Assert(reqs[2].URL.Path, check.Equals, "/resources/my-mysql/bind")
	siDB, err := GetServiceInstance(si.ServiceName, si.Name)
	c.Assert(err, check.IsNil)
	c.Assert(siDB.Apps, check.DeepEquals, []string{"myapp"})
	c.Assert(a.GetServiceEnvs(), check.DeepEquals, []bind.ServiceEnvVar{
		{EnvVar: bind.EnvVar{Name: "ENV1", Value: "VAL1"}, ServiceName: "mysql", InstanceName: "my-mysql"},
		{EnvVar: bind.EnvVar{Name: "ENV2", Value: "VAL2"}, ServiceName: "mysql", InstanceName: "my-mysql"},
	})
}

func (s *InstanceSuite) TestBindAppMultipleApps(c *check.C) {
	goMaxProcs := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(goMaxProcs)
	var reqs []*http.Request
	var mut sync.Mutex
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mut.Lock()
		defer mut.Unlock()
		reqs = append(reqs, r)
		w.WriteHeader(http.StatusOK)
		if r.URL.Path == "/resources/my-mysql/bind-app" && r.Method == "POST" {
			w.Write([]byte(`{"ENV1": "VAL1", "ENV2": "VAL2"}`))
		}
	}))
	defer ts.Close()
	serv := Service{Name: "mysql", Endpoint: map[string]string{"production": ts.URL}, Password: "s3cr3t"}
	err := serv.Create()
	c.Assert(err, check.IsNil)
	si := ServiceInstance{
		Name:        "my-mysql",
		ServiceName: "mysql",
		Teams:       []string{s.team.Name},
	}
	err = si.Create()
	c.Assert(err, check.IsNil)
	var apps []bind.App
	var expectedNames []string
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("myapp-%02d", i)
		expectedNames = append(expectedNames, name)
		apps = append(apps, provisiontest.NewFakeApp(name, "static", 2))
	}
	wg := sync.WaitGroup{}
	for _, app := range apps {
		wg.Add(1)
		go func(app bind.App) {
			defer wg.Done()
			var buf bytes.Buffer
			bindErr := si.BindApp(app, true, &buf)
			c.Assert(bindErr, check.IsNil)
		}(app)
	}
	wg.Wait()
	c.Assert(reqs, check.HasLen, 300)
	siDB, err := GetServiceInstance(si.ServiceName, si.Name)
	c.Assert(err, check.IsNil)
	sort.Strings(siDB.Apps)
	c.Assert(siDB.Apps, check.DeepEquals, expectedNames)
}

func (s *InstanceSuite) TestUnbindAppMultipleApps(c *check.C) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(4))
	var reqs []*http.Request
	var mut sync.Mutex
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mut.Lock()
		defer mut.Unlock()
		reqs = append(reqs, r)
		w.WriteHeader(http.StatusOK)
		if r.URL.Path == "/resources/my-mysql/bind-app" && r.Method == "POST" {
			w.Write([]byte(`{"ENV1": "VAL1", "ENV2": "VAL2"}`))
		}
	}))
	defer ts.Close()
	serv := Service{Name: "mysql", Endpoint: map[string]string{"production": ts.URL}, Password: "s3cr3t"}
	err := serv.Create()
	c.Assert(err, check.IsNil)
	si := ServiceInstance{
		Name:        "my-mysql",
		ServiceName: "mysql",
		Teams:       []string{s.team.Name},
	}
	err = si.Create()
	c.Assert(err, check.IsNil)
	var apps []bind.App
	for i := 0; i < 20; i++ {
		name := fmt.Sprintf("myapp-%02d", i)
		app := provisiontest.NewFakeApp(name, "static", 2)
		apps = append(apps, app)
		var buf bytes.Buffer
		err = si.BindApp(app, true, &buf)
		c.Assert(err, check.IsNil)
	}
	siDB, err := GetServiceInstance(si.ServiceName, si.Name)
	c.Assert(err, check.IsNil)
	wg := sync.WaitGroup{}
	for _, app := range apps {
		wg.Add(1)
		go func(app bind.App) {
			defer wg.Done()
			var buf bytes.Buffer
			unbindErr := siDB.UnbindApp(app, false, &buf)
			c.Assert(unbindErr, check.IsNil)
		}(app)
	}
	wg.Wait()
	c.Assert(reqs, check.HasLen, 120)
	siDB, err = GetServiceInstance(si.ServiceName, si.Name)
	c.Assert(err, check.IsNil)
	sort.Strings(siDB.Apps)
	c.Assert(siDB.Apps, check.DeepEquals, []string{})
}

func (s *S) TestRenameServiceInstanceTeam(c *check.C) {
	sInstances := []ServiceInstance{
		{Name: "si1", ServiceName: "mysql", Teams: []string{"team1", "team2", "team3"}, TeamOwner: "team1"},
		{Name: "si2", ServiceName: "mysql", Teams: []string{"team1", "team3"}, TeamOwner: "team2"},
		{Name: "si3", ServiceName: "mysql", Teams: []string{"team2", "team3"}, TeamOwner: "team3"},
	}
	for _, si := range sInstances {
		err := s.conn.ServiceInstances().Insert(&si)
		c.Assert(err, check.IsNil)
	}
	err := RenameServiceInstanceTeam("team2", "team9000")
	c.Assert(err, check.IsNil)
	var dbInstances []ServiceInstance
	err = s.conn.ServiceInstances().Find(nil).Sort("name").All(&dbInstances)
	c.Assert(err, check.IsNil)
	c.Assert(dbInstances, check.DeepEquals, []ServiceInstance{
		{Name: "si1", ServiceName: "mysql", Teams: []string{"team1", "team3", "team9000"}, TeamOwner: "team1", Apps: []string{}, BoundUnits: []Unit{}, Tags: []string{}},
		{Name: "si2", ServiceName: "mysql", Teams: []string{"team1", "team3"}, TeamOwner: "team9000", Apps: []string{}, BoundUnits: []Unit{}, Tags: []string{}},
		{Name: "si3", ServiceName: "mysql", Teams: []string{"team3", "team9000"}, TeamOwner: "team3", Apps: []string{}, BoundUnits: []Unit{}, Tags: []string{}},
	})
}
