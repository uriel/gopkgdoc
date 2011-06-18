// Copyright 2011 Gary Burd
//
// Licensed under the Apache License, Version 2.0 (the "License"): you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
// WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
// License for the specific language governing permissions and limitations
// under the License.

// The functions in this file use a deleted sentinel value and CAS to avoid
// writing stale data to the cache.

package app

import (
	"appengine"
	"appengine/memcache"
	"gob"
	"bytes"
)

func cacheGet(c appengine.Context, key string, value interface{}) (*memcache.Item, bool) {
	item, err := memcache.Get(c, key)
	if err != nil {
		if err != memcache.ErrCacheMiss {
			c.Errorf("cache: error fetching %s from cache, %v", key, err)
		}
		c.Infof("cache: cache miss for %s (empty)", key)
		return &memcache.Item{Key: key}, false
	}
	// If it's the deleted sentinel value, then treat it as a miss.
	if len(item.Value) == 1 && item.Value[0] == 0 {
		c.Infof("cache: cache miss for %s (deleted sentinel)", key)
		return item, false
	}
	err = gob.NewDecoder(bytes.NewBuffer(item.Value)).Decode(value)
	if err != nil {
		c.Errorf("cache: error decoding %s value, %v", key, err)
		return item, false
	}
	return item, true
}

func cacheClear(c appengine.Context, key string) {
	// Set the deleted sentinel value.
	err := memcache.Set(c, &memcache.Item{Key: key, Value: []byte{0}, Expiration: 60})
	if err != nil {
		c.Errorf("cache: error setting %s to sentinel value, %v", key, err)
	}
}

func cacheSet(c appengine.Context, item *memcache.Item, expiration int32, value interface{}) {
	var buf bytes.Buffer
	err := gob.NewEncoder(&buf).Encode(value)
	if err != nil {
		c.Errorf("cache: error encoding value for %s, %v", item.Key, err)
		return
	}
	add := item.Value == nil
	item.Value = buf.Bytes()
	item.Expiration = expiration
	if add {
		err = memcache.Add(c, item)
	} else if appengine.IsDevAppServer() {
		// CAS does not work in dev environment, use set instead.
		err = memcache.Set(c, item)
	} else {
		err = memcache.CompareAndSwap(c, item)
	}
	if err != nil && err != memcache.ErrNotStored {
		c.Errorf("cache: error updating value for %s, %v", item.Key, err)
		return
	}
}
