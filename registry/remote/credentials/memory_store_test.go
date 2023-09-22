/*
   Copyright The ORAS Authors.
   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package credentials

import (
	"context"
	"reflect"
	"testing"

	"oras.land/oras-go/v2/registry/remote/auth"
)

func TestMemoryStore_Get_notExistRecord(t *testing.T) {
	ctx := context.Background()
	ms := NewMemoryStore()

	serverAddress := "registry.example.com"
	got, err := ms.Get(ctx, serverAddress)
	if err != nil {
		t.Errorf("MemoryStore.Get() error = %v", err)
		return
	}
	if !reflect.DeepEqual(got, auth.EmptyCredential) {
		t.Errorf("MemoryStore.Get() = %v, want %v", got, auth.EmptyCredential)
	}
}

func TestMemoryStore_Get_validRecord(t *testing.T) {
	ctx := context.Background()
	ms := NewMemoryStore().(*memoryStore)

	serverAddress := "registry.example.com"
	want := auth.Credential{
		Username:     "username",
		Password:     "password",
		RefreshToken: "identity_token",
		AccessToken:  "registry_token",
	}
	ms.store.Store(serverAddress, want)

	got, err := ms.Get(ctx, serverAddress)
	if err != nil {
		t.Errorf("MemoryStore.Get() error = %v", err)
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MemoryStore.Get() = %v, want %v", got, want)
	}
}

func TestMemoryStore_Put_addNew(t *testing.T) {
	ctx := context.Background()
	ms := NewMemoryStore()

	// Test Put
	server1 := "registry.example.com"
	cred1 := auth.Credential{
		Username:     "username",
		Password:     "password",
		RefreshToken: "identity_token",
		AccessToken:  "registry_token",
	}
	if err := ms.Put(ctx, server1, cred1); err != nil {
		t.Errorf("MemoryStore.Put() error = %v", err)
		return
	}

	server2 := "registry2.example.com"
	cred2 := auth.Credential{
		Username:     "username2",
		Password:     "password2",
		RefreshToken: "identity_token2",
		AccessToken:  "registry_token2",
	}
	if err := ms.Put(ctx, server2, cred2); err != nil {
		t.Errorf("MemoryStore.Put() error = %v", err)
		return
	}

	// Verify Content
	got1, err := ms.Get(ctx, server1)
	if err != nil {
		t.Errorf("MemoryStore.Get() error = %v", err)
		return
	}
	if !reflect.DeepEqual(got1, cred1) {
		t.Errorf("MemoryStore.Get() = %v, want %v", got1, cred1)
		return
	}

	got2, err := ms.Get(ctx, server2)
	if err != nil {
		t.Errorf("MemoryStore.Get() error = %v", err)
		return
	}
	if !reflect.DeepEqual(got2, cred2) {
		t.Errorf("MemoryStore.Get() = %v, want %v", got2, cred2)
		return
	}
}

func TestMemoryStore_Put_update(t *testing.T) {
	ctx := context.Background()
	ms := NewMemoryStore()

	// Test Put
	serverAddress := "registry.example.com"
	cred1 := auth.Credential{
		Username:     "username",
		Password:     "password",
		RefreshToken: "identity_token",
		AccessToken:  "registry_token",
	}
	if err := ms.Put(ctx, serverAddress, cred1); err != nil {
		t.Errorf("MemoryStore.Put() error = %v", err)
		return
	}

	cred2 := auth.Credential{
		Username:     "username2",
		Password:     "password2",
		RefreshToken: "identity_token2",
		AccessToken:  "registry_token2",
	}
	if err := ms.Put(ctx, serverAddress, cred2); err != nil {
		t.Errorf("MemoryStore.Put() error = %v", err)
		return
	}

	got, err := ms.Get(ctx, serverAddress)
	if err != nil {
		t.Errorf("MemoryStore.Get() error = %v", err)
		return
	}
	if !reflect.DeepEqual(got, cred2) {
		t.Errorf("MemoryStore.Get() = %v, want %v", got, cred2)
		return
	}
}

func TestMemoryStore_Delete_existRecord(t *testing.T) {
	ctx := context.Background()
	ms := NewMemoryStore()

	// Test Put
	serverAddress := "registry.example.com"
	cred := auth.Credential{
		Username:     "username",
		Password:     "password",
		RefreshToken: "identity_token",
		AccessToken:  "registry_token",
	}
	if err := ms.Put(ctx, serverAddress, cred); err != nil {
		t.Errorf("MemoryStore.Put() error = %v", err)
		return
	}

	// Test Get
	got, err := ms.Get(ctx, serverAddress)
	if err != nil {
		t.Errorf("MemoryStore.Get() error = %v", err)
		return
	}
	if !reflect.DeepEqual(got, cred) {
		t.Errorf("MemoryStore.Get(%s) = %v, want %v", serverAddress, got, cred)
		return
	}

	// Test Delete
	if err := ms.Delete(ctx, serverAddress); err != nil {
		t.Errorf("MemoryStore.Delete() error = %v", err)
		return
	}

	// Test Get again
	got, err = ms.Get(ctx, serverAddress)
	if err != nil {
		t.Errorf("MemoryStore.Get() error = %v", err)
		return
	}
	if !reflect.DeepEqual(got, auth.EmptyCredential) {
		t.Errorf("MemoryStore.Get() = %v, want %v", got, auth.EmptyCredential)
		return
	}
}

func TestMemoryStore_Delete_notExistRecord(t *testing.T) {
	ctx := context.Background()
	ms := NewMemoryStore()

	// Test Put
	serverAddress := "registry.example.com"
	cred := auth.Credential{
		Username:     "username",
		Password:     "password",
		RefreshToken: "identity_token",
		AccessToken:  "registry_token",
	}
	if err := ms.Put(ctx, serverAddress, cred); err != nil {
		t.Errorf("MemoryStore.Put() error = %v", err)
		return
	}

	// Test Delete
	if err := ms.Delete(ctx, serverAddress); err != nil {
		t.Errorf("MemoryStore.Delete() error = %v", err)
		return
	}

	// Test Delete again
	// Expect no error if target record does not exist
	if err := ms.Delete(ctx, serverAddress); err != nil {
		t.Errorf("MemoryStore.Delete() error = %v", err)
		return
	}
}
