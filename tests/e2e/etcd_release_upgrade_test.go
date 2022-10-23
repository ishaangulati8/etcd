// Copyright 2016 The etcd Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package e2e

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"go.etcd.io/etcd/api/v3/version"
	"go.etcd.io/etcd/client/pkg/v3/fileutil"
	"go.etcd.io/etcd/tests/v3/framework/config"
	"go.etcd.io/etcd/tests/v3/framework/e2e"
)

// TestReleaseUpgrade ensures that changes to master branch does not affect
// upgrade from latest etcd releases.
func TestReleaseUpgrade(t *testing.T) {
	if !fileutil.Exist(e2e.BinPath.EtcdLastRelease) {
		t.Skipf("%q does not exist", e2e.BinPath.EtcdLastRelease)
	}

	e2e.BeforeTest(t)

	copiedCfg := e2e.NewConfigNoTLS()
	copiedCfg.Version = config.LastVersion
	copiedCfg.SnapshotCount = 3
	copiedCfg.BaseScheme = "unix" // to avoid port conflict

	epc, err := e2e.NewEtcdProcessCluster(context.TODO(), t, copiedCfg)
	if err != nil {
		t.Fatalf("could not start etcd process cluster (%v)", err)
	}
	defer func() {
		if errC := epc.Close(); errC != nil {
			t.Fatalf("error closing etcd processes (%v)", errC)
		}
	}()

	cx := ctlCtx{
		t:           t,
		cfg:         *e2e.NewConfigNoTLS(),
		dialTimeout: 7 * time.Second,
		quorum:      true,
		epc:         epc,
	}
	var kvs []kv
	for i := 0; i < 5; i++ {
		kvs = append(kvs, kv{key: fmt.Sprintf("foo%d", i), val: "bar"})
	}
	for i := range kvs {
		if err := ctlV3Put(cx, kvs[i].key, kvs[i].val, ""); err != nil {
			cx.t.Fatalf("#%d: ctlV3Put error (%v)", i, err)
		}
	}

	t.Log("Cluster of etcd in old version running")

	for i := range epc.Procs {
		t.Logf("Stopping node: %v", i)
		if err := epc.Procs[i].Stop(); err != nil {
			t.Fatalf("#%d: error closing etcd process (%v)", i, err)
		}
		t.Logf("Stopped node: %v", i)
		epc.Procs[i].Config().ExecPath = e2e.BinPath.Etcd
		epc.Procs[i].Config().KeepDataDir = true

		t.Logf("Restarting node in the new version: %v", i)
		if err := epc.Procs[i].Restart(context.TODO()); err != nil {
			t.Fatalf("error restarting etcd process (%v)", err)
		}

		t.Logf("Testing reads after node restarts: %v", i)
		for j := range kvs {
			if err := ctlV3Get(cx, []string{kvs[j].key}, []kv{kvs[j]}...); err != nil {
				cx.t.Fatalf("#%d-%d: ctlV3Get error (%v)", i, j, err)
			}
		}
		t.Logf("Tested reads after node restarts: %v", i)
	}

	t.Log("Waiting for full upgrade...")
	// TODO: update after release candidate
	// expect upgraded cluster version
	// new cluster version needs more time to upgrade
	ver := version.Cluster(version.Version)
	for i := 0; i < 7; i++ {
		if err = e2e.CURLGet(epc, e2e.CURLReq{Endpoint: "/version", Expected: `"etcdcluster":"` + ver}); err != nil {
			t.Logf("#%d: %v is not ready yet (%v)", i, ver, err)
			time.Sleep(time.Second)
			continue
		}
		break
	}
	if err != nil {
		t.Fatalf("cluster version is not upgraded (%v)", err)
	}
	t.Log("TestReleaseUpgrade businessLogic DONE")
}

func TestReleaseUpgradeWithRestart(t *testing.T) {
	if !fileutil.Exist(e2e.BinPath.EtcdLastRelease) {
		t.Skipf("%q does not exist", e2e.BinPath.EtcdLastRelease)
	}

	e2e.BeforeTest(t)

	copiedCfg := e2e.NewConfigNoTLS()
	copiedCfg.Version = config.LastVersion
	copiedCfg.SnapshotCount = 10
	copiedCfg.BaseScheme = "unix"

	epc, err := e2e.NewEtcdProcessCluster(context.TODO(), t, copiedCfg)
	if err != nil {
		t.Fatalf("could not start etcd process cluster (%v)", err)
	}
	defer func() {
		if errC := epc.Close(); errC != nil {
			t.Fatalf("error closing etcd processes (%v)", errC)
		}
	}()

	cx := ctlCtx{
		t:           t,
		cfg:         *e2e.NewConfigNoTLS(),
		dialTimeout: 7 * time.Second,
		quorum:      true,
		epc:         epc,
	}
	var kvs []kv
	for i := 0; i < 50; i++ {
		kvs = append(kvs, kv{key: fmt.Sprintf("foo%d", i), val: "bar"})
	}
	for i := range kvs {
		if err := ctlV3Put(cx, kvs[i].key, kvs[i].val, ""); err != nil {
			cx.t.Fatalf("#%d: ctlV3Put error (%v)", i, err)
		}
	}

	for i := range epc.Procs {
		if err := epc.Procs[i].Stop(); err != nil {
			t.Fatalf("#%d: error closing etcd process (%v)", i, err)
		}
	}

	var wg sync.WaitGroup
	wg.Add(len(epc.Procs))
	for i := range epc.Procs {
		go func(i int) {
			epc.Procs[i].Config().ExecPath = e2e.BinPath.Etcd
			epc.Procs[i].Config().KeepDataDir = true
			if err := epc.Procs[i].Restart(context.TODO()); err != nil {
				t.Errorf("error restarting etcd process (%v)", err)
			}
			wg.Done()
		}(i)
	}
	wg.Wait()

	if err := ctlV3Get(cx, []string{kvs[0].key}, []kv{kvs[0]}...); err != nil {
		t.Fatal(err)
	}
}
