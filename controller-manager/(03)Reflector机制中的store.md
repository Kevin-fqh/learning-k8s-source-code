# Reflector机制中的store

**Table of Contents**
<!-- BEGIN MUNGE: GENERATED_TOC -->
  - [定义](#定义)
  - [ThreadSafeStore](#threadsafestore)
  - [type threadSafeMap struct 用法](#type-threadsafemap-struct-用法)
  - [type cache struct](#type-cache-struct)
  - [Demo](#demo)
<!-- END MUNGE: GENERATED_TOC -->

## 定义
Store和Indexer 是Kubernetes用来存储obj的结构体。 
与reflector机制配合使用，可以把ETCD中的变化同步到Store和Indexer中，在代码中可以直接从Store和Indexer获取相应的对象。 
也即是说，Store和Indexer是ETCD中内容在内存中的一个缓存。

Store 有好几种，只要满足 Store接口的，都可以叫 Store：如delta_fifo、fifo、cache等
```go
// Store is a generic object storage interface. Reflector knows how to watch a server
// and update a store. A generic store is provided, which allows Reflector to be used
// as a local caching system, and an LRU store, which allows Reflector to work like a
// queue of items yet to be processed.
//
// Store makes no assumptions about stored object identity; it is the responsibility
// of a Store implementation to provide a mechanism to correctly key objects and to
// define the contract for obtaining objects by some arbitrary key type.
type Store interface {
	/*
		Store 有好几种，只要满足 Store接口的，都可以叫 Store：
		如delta_fifo、fifo、cache等
	*/
	Add(obj interface{}) error
	Update(obj interface{}) error
	Delete(obj interface{}) error
	List() []interface{}
	ListKeys() []string
	Get(obj interface{}) (item interface{}, exists bool, err error)
	GetByKey(key string) (item interface{}, exists bool, err error)

	// Replace will delete the contents of the store, using instead the
	// given list. Store takes ownership of the list, you should not reference
	// it after calling this function.
	Replace([]interface{}, string) error
	Resync() error
}
```

Indexer是在Store的基础上增加了index func管理的功能, lets you list objects using multiple indexing functions.
```go
// Indexer is a storage interface that lets you list objects using multiple indexing functions
type Indexer interface {
	Store
	// Retrieve list of objects that match on the named indexing function
	Index(indexName string, obj interface{}) ([]interface{}, error)
	// ListIndexFuncValues returns the list of generated values of an Index func
	ListIndexFuncValues(indexName string) []string
	// ByIndex lists object that match on the named indexing function with the exact key
	ByIndex(indexName, indexKey string) ([]interface{}, error)
	// GetIndexer return the indexers
	GetIndexers() Indexers

	// AddIndexers adds more indexers to this store.  If you call this after you already have data
	// in the store, the results are undefined.
	AddIndexers(newIndexers Indexers) error
}
```

- NewStore 和 NewIndexer

Store和Indexer都是一个cache，其本质都是一个threadSafeStore。 
不同的是Store的Indexers参数为空，而Indexer的Indexers参数有值。
```go
// NewStore returns a Store implemented simply with a map and a lock.
func NewStore(keyFunc KeyFunc) Store {
	return &cache{
		cacheStorage: NewThreadSafeStore(Indexers{}, Indices{}),
		keyFunc:      keyFunc,
	}
}

// NewIndexer returns an Indexer implemented simply with a map and a lock.
/*
	Indexer是在Store的基础上增加了index func管理的功能
*/
func NewIndexer(keyFunc KeyFunc, indexers Indexers) Indexer {
	return &cache{
		cacheStorage: NewThreadSafeStore(indexers, Indices{}),
		keyFunc:      keyFunc,
	}
}
```

## ThreadSafeStore  
ThreadSafeStore是底层具体负责对象存储的结构体，其具体由threadSafeMap实现。
```go
func NewThreadSafeStore(indexers Indexers, indices Indices) ThreadSafeStore {
	return &threadSafeMap{
		items:    map[string]interface{}{},
		indexers: indexers,
		indices:  indices,
	}
}
```

在ThreadSafeStore中，Index存放的并不是obj，而是obj的key。 
而items中存入的就是key和obj。 
所以说，在ThreadSafeStore中的Index只负责obj的key的管理。

## type threadSafeMap struct 用法
1. 在threadSafemap中，首先通过indexers[name]获取IndexFunc，然后使用IndexFunc计算obj的indexkey。
2. 然后通过Indices[name]获取具体的Index；结合indexkey，就可以获取到具体obj的key，然后在items[key]获取obj的具体值。
3. IndexFunc是threadSafemap使用

```go
type threadSafeMap struct {
	lock  sync.RWMutex
	items map[string]interface{} //key和obj的map

	// indexers maps a name to an IndexFunc
	/*
		type Indexers map[string]IndexFunc
		可以从indexers中找到对应的indexFunc
	*/
	indexers Indexers
	// indices maps a name to an Index
	/*
		type Index map[string]sets.String
	*/
	indices Indices
}
```

来看看threadSafeMap实现的功能函数
```go

/*
	Add()先更新items map中的值，然后调用updateIndices()更新index。
*/
func (c *threadSafeMap) Add(key string, obj interface{}) {
	//先把oldObject找到（有的话）；然后更新该key对应的值。最后处理索引。
	c.lock.Lock()
	defer c.lock.Unlock()
	oldObject := c.items[key]
	c.items[key] = obj
	c.updateIndices(oldObject, obj, key)
}

func (c *threadSafeMap) Update(key string, obj interface{}) {
	c.lock.Lock()
	defer c.lock.Unlock()
	oldObject := c.items[key]
	c.items[key] = obj
	c.updateIndices(oldObject, obj, key)
}

/*
	Delete()先把key从index中删除，然后再把key从items中删除。
*/
func (c *threadSafeMap) Delete(key string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if obj, exists := c.items[key]; exists {
		c.deleteFromIndices(obj, key)
		delete(c.items, key)
	}
}

/*
	Get()依据key从items中获取item，然后返回。
*/
func (c *threadSafeMap) Get(key string) (item interface{}, exists bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	item, exists = c.items[key]
	return item, exists
}

/*
	List()返回items map中的所有value。
*/
func (c *threadSafeMap) List() []interface{} {
	c.lock.RLock()
	defer c.lock.RUnlock()
	list := make([]interface{}, 0, len(c.items))
	for _, item := range c.items {
		list = append(list, item)
	}
	return list
}

// ListKeys returns a list of all the keys of the objects currently
// in the threadSafeMap.
/*
	ListKeys()返回items map中的所有key。
*/
func (c *threadSafeMap) ListKeys() []string {
	c.lock.RLock()
	defer c.lock.RUnlock()
	list := make([]string, 0, len(c.items))
	for key := range c.items {
		list = append(list, key)
	}
	return list
}

/*
	Replace()重新构建items和index。
*/
func (c *threadSafeMap) Replace(items map[string]interface{}, resourceVersion string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.items = items

	// rebuild any index
	c.indices = Indices{}
	for key, item := range c.items {
		c.updateIndices(nil, item, key)
	}
}

// Index returns a list of items that match on the index function
// Index is thread-safe so long as you treat all items as immutable
/*
	Index()返回先使用indexName获取indexFunc，
	然后计算出obj的indexValue，
	再取出indexValue对应的keys，
	最后依据keys从items中获取对应的obj并返回。
*/
func (c *threadSafeMap) Index(indexName string, obj interface{}) ([]interface{}, error) {
	c.lock.RLock()
	defer c.lock.RUnlock()

	//从indexers找到indexFunc
	indexFunc := c.indexers[indexName]
	if indexFunc == nil {
		return nil, fmt.Errorf("Index with name %s does not exist", indexName)
	}
	//使用indexFunc计算obj的indexKey
	indexKeys, err := indexFunc(obj)
	if err != nil {
		return nil, err
	}
	index := c.indices[indexName]

	// need to de-dupe the return list.  Since multiple keys are allowed, this can happen.
	returnKeySet := sets.String{}
	for _, indexKey := range indexKeys {
		//通过indexKey从index中获取具体的key
		set := index[indexKey]
		for _, key := range set.UnsortedList() {
			returnKeySet.Insert(key)
		}
	}

	list := make([]interface{}, 0, returnKeySet.Len())
	for absoluteKey := range returnKeySet {
		list = append(list, c.items[absoluteKey])
	}
	return list, nil
}

// ByIndex returns a list of items that match an exact value on the index function
/*
	ByIndex()和Index()类似，不同的是其传入的是indexKey，无需计算。
*/
func (c *threadSafeMap) ByIndex(indexName, indexKey string) ([]interface{}, error) {
	c.lock.RLock()
	defer c.lock.RUnlock()

	indexFunc := c.indexers[indexName]
	if indexFunc == nil {
		return nil, fmt.Errorf("Index with name %s does not exist", indexName)
	}

	index := c.indices[indexName]

	set := index[indexKey]
	list := make([]interface{}, 0, set.Len())
	for _, key := range set.List() {
		list = append(list, c.items[key])
	}

	return list, nil
}

/*
	ListIndexFuncValues()返回indexName对应的index map中的keys。
	可以理解为indexName分类函数对objs划分的类别。
*/
func (c *threadSafeMap) ListIndexFuncValues(indexName string) []string {
	c.lock.RLock()
	defer c.lock.RUnlock()

	index := c.indices[indexName]
	names := make([]string, 0, len(index))
	for key := range index {
		names = append(names, key)
	}
	return names
}

/*
	GetIndexers()返回indexers。
	indexers保存indexName和indexFunc的关系。
*/
func (c *threadSafeMap) GetIndexers() Indexers {
	//获取cache中所有indexers, Indexers的类型为map[string]IndexFunc
	return c.indexers
}

/*
	AddIndexers()把indexFunc添加到threadSafeMap中。
*/
func (c *threadSafeMap) AddIndexers(newIndexers Indexers) error {
	c.lock.Lock()
	defer c.lock.Unlock()

	if len(c.items) > 0 {
		return fmt.Errorf("cannot add indexers to running index")
	}

	oldKeys := sets.StringKeySet(c.indexers)
	newKeys := sets.StringKeySet(newIndexers)

	if oldKeys.HasAny(newKeys.List()...) {
		return fmt.Errorf("indexer conflict: %v", oldKeys.Intersection(newKeys))
	}

	for k, v := range newIndexers {
		c.indexers[k] = v
	}
	return nil
}

// updateIndices modifies the objects location in the managed indexes, if this is an update, you must provide an oldObj
// updateIndices must be called from a function that already has a lock on the cache
/*
	updateIndices()先把oldObj从索引中删除，然后把newObj的key添加到索引中。
*/
func (c *threadSafeMap) updateIndices(oldObj interface{}, newObj interface{}, key string) error {
	// if we got an old object, we need to remove it before we add it again
	/*
		函数要先把oldObj先删除，然后再把newObj添加，
		因为每个obj都会依据indexFunc()计算出索引，每个obj的索引都不一样
	*/
	if oldObj != nil {
		c.deleteFromIndices(oldObj, key)
	}
	/*
		indexers存储了name和indexFunc的关系
		在NewStore()中，NewThreadSafeStore(Indexers{}, Indices{})，Indexers{}未进行初始化、所以在store相关操作中，并未执行该循环。
		因为Store的实现cache，已经包含了KeyFunc
	*/
	for name, indexFunc := range c.indexers {
		indexValues, err := indexFunc(newObj)
		if err != nil {
			return err
		}
		//indices存储了name和index的关系
		index := c.indices[name]
		if index == nil {
			index = Index{}
			c.indices[name] = index
		}

		//index存储了indexValue和keys的关系
		for _, indexValue := range indexValues {
			set := index[indexValue]
			if set == nil {
				set = sets.String{}
				index[indexValue] = set
			}
			set.Insert(key)
		}
	}
	return nil
}

// deleteFromIndices removes the object from each of the managed indexes
// it is intended to be called from a function that already has a lock on the cache
/*
	deleteFromIndices()把obj对应的key从index中删除。
*/
func (c *threadSafeMap) deleteFromIndices(obj interface{}, key string) error {
	for name, indexFunc := range c.indexers {
		indexValues, err := indexFunc(obj)
		if err != nil {
			return err
		}

		index := c.indices[name]
		if index == nil {
			continue
		}
		for _, indexValue := range indexValues {
			set := index[indexValue]
			if set != nil {
				set.Delete(key)
			}
		}
	}
	return nil
}
```

## type cache struct
Cache是ThreadSafeStore再加上一个keyFunc。 
所有的obj会先使用keyFunc进行计算得到key，然后把obj和key存入到ThreadSafeStore中。
```go
type cache struct {
	// cacheStorage bears the burden of thread safety for the cache
	cacheStorage ThreadSafeStore
	// keyFunc is used to make the key for objects stored in and retrieved from items, and
	// should be deterministic.
	keyFunc KeyFunc
}
```

## Demo
```go
package main
import (
	"fmt"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/client/cache"
)
func testIndexFunc(obj interface{}) ([]string, error) {
	pod := obj.(*api.Pod)
	return []string{pod.Labels["foo"]}, nil
}
func GetIndexFuncValues() {
	index := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{"testmodes": testIndexFunc})
	pod1 := &api.Pod{ObjectMeta: api.ObjectMeta{Name: "one", Labels: map[string]string{"foo": "bar"}}}
	pod2 := &api.Pod{ObjectMeta: api.ObjectMeta{Name: "two", Labels: map[string]string{"foo": "bar"}}}
	pod3 := &api.Pod{ObjectMeta: api.ObjectMeta{Name: "three", Labels: map[string]string{"foo": "biz"}}}
	index.Add(pod1)
	index.Add(pod2)
	index.Add(pod3)
	keys := index.ListIndexFuncValues("testmodes")
	for _, key := range keys {
		fmt.Println("key:", key)
		items, _ := index.ByIndex("testmodes", key)
		for _, item := range items {
			fmt.Println("pod", item.(*api.Pod).ObjectMeta.Name)
		}
	}
}
func main() {
	GetIndexFuncValues()
}
```
输出如下：
```go
key: bar
pod one
pod two
key: biz
pod three
```


