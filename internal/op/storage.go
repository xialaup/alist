package op

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/alist-org/alist/v3/internal/conf"
	"github.com/alist-org/alist/v3/internal/db"
	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/pkg/generic_sync"
	"github.com/alist-org/alist/v3/pkg/utils"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
)

// Although the driver type is stored,
// there is a storage in each driver,
// so it should actually be a storage, just wrapped by the driver
var storagesMap generic_sync.MapOf[string, driver.Driver]

func GetAllStorages() []driver.Driver {
	return storagesMap.Values()
}

func HasStorage(mountPath string) bool {
	return storagesMap.Has(utils.FixAndCleanPath(mountPath))
}

func GetStorageByMountPath(mountPath string) (driver.Driver, error) {
	mountPath = utils.FixAndCleanPath(mountPath)
	storageDriver, ok := storagesMap.Load(mountPath)
	if !ok {
		return nil, errors.Errorf("no mount path for an storage is: %s", mountPath)
	}
	return storageDriver, nil
}

// CreateStorage Save the storage to database so storage can get an id
// then instantiate corresponding driver and save it in memory
func CreateStorage(ctx context.Context, storage model.Storage) (uint, error) {
	storage.Modified = time.Now()
	storage.MountPath = utils.FixAndCleanPath(storage.MountPath)
	var err error
	// check driver first
	driverName := storage.Driver
	driverNew, err := GetDriver(driverName)
	if err != nil {
		return 0, errors.WithMessage(err, "failed get driver new")
	}
	storageDriver := driverNew()
	// insert storage to database
	err = db.CreateStorage(&storage)
	if err != nil {
		return storage.ID, errors.WithMessage(err, "failed create storage in database")
	}
	// already has an id
	err = initStorage(ctx, storage, storageDriver)
	go callStorageHooks("add", storageDriver)
	if err != nil {
		return storage.ID, errors.Wrap(err, "failed init storage but storage is already created")
	}
	log.Debugf("storage %+v is created", storageDriver)
	return storage.ID, nil
}

// 根据ID复制存储
func CopyStorageById(ctx context.Context, id uint) (uint, error) {
	storage, err := db.GetStorageById(id)
	if err != nil {
		return 0, errors.WithMessage(err, "copied get storage")
	}
	// 将 Storage 转换为 JSON
	jsonData, _ := json.Marshal(storage)
	storage_json := string(jsonData)
	var data map[string]interface{}
	err = json.Unmarshal([]byte(storage_json), &data)
	if err != nil {
		return 0, errors.WithMessage(err, "解析存储失败")
	}
	// Step 2: 移除ID字段
	delete(data, "id")
	// Step 3: 将修改后的数据结构编码为 JSON 字符串
	result, err := json.Marshal(data)
	if err != nil {
		return 0, errors.WithMessage(err, "解析存储失败")
	}
	var new_storage model.Storage
	err = json.Unmarshal([]byte(result), &new_storage)
	if err != nil {
		return 0, errors.WithMessage(err, "解析新存储失败")
	}

	// check driver first
	new_storage.MountPath = storage.MountPath + "_copyed2"
	new_storage.Modified = time.Now()
	new_storage.MountPath = utils.FixAndCleanPath(new_storage.MountPath)
	driverName := new_storage.Driver
	driverNew, err := GetDriver(driverName)
	if err != nil {
		return 0, errors.WithMessage(err, "failed get driver new")
	}
	storageDriver := driverNew()
	// insert storage to database
	err = db.CreateStorage(&new_storage)
	if err != nil {
		return new_storage.ID, errors.WithMessage(err, "failed create storage in database")
	}
	// already has an id
	err = initStorage(ctx, new_storage, storageDriver)
	go callStorageHooks("add", storageDriver)
	if err != nil {
		return new_storage.ID, errors.Wrap(err, "failed init storage but storage is already created")
	}
	log.Debugf("storage %+v is created", storageDriver)
	return new_storage.ID, nil
}

// LoadStorage load exist storage in db to memory
func LoadStorage(ctx context.Context, storage model.Storage) error {
	storage.MountPath = utils.FixAndCleanPath(storage.MountPath)
	// check driver first
	driverName := storage.Driver
	driverNew, err := GetDriver(driverName)
	if err != nil {
		return errors.WithMessage(err, "failed get driver new")
	}
	storageDriver := driverNew()

	err = initStorage(ctx, storage, storageDriver)
	go callStorageHooks("add", storageDriver)
	log.Debugf("storage %+v is created", storageDriver)
	return err
}

// initStorage initialize the driver and store to storagesMap
func initStorage(ctx context.Context, storage model.Storage, storageDriver driver.Driver) (err error) {
	storageDriver.SetStorage(storage)
	driverStorage := storageDriver.GetStorage()

	// Unmarshal Addition
	err = utils.Json.UnmarshalFromString(driverStorage.Addition, storageDriver.GetAddition())
	if err == nil {
		err = storageDriver.Init(ctx)
	}
	storagesMap.Store(driverStorage.MountPath, storageDriver)
	if err != nil {
		driverStorage.SetStatus(err.Error())
		err = errors.Wrap(err, "failed init storage")
	} else {
		driverStorage.SetStatus(WORK)
	}
	MustSaveDriverStorage(storageDriver)
	return err
}

func EnableStorage(ctx context.Context, id uint) error {
	storage, err := db.GetStorageById(id)
	if err != nil {
		return errors.WithMessage(err, "failed get storage")
	}
	if !storage.Disabled {
		return errors.Errorf("this storage have enabled")
	}
	storage.Disabled = false
	err = db.UpdateStorage(storage)
	if err != nil {
		return errors.WithMessage(err, "failed update storage in db")
	}
	err = LoadStorage(ctx, *storage)
	if err != nil {
		return errors.WithMessage(err, "failed load storage")
	}
	return nil
}

func DisableStorage(ctx context.Context, id uint) error {
	storage, err := db.GetStorageById(id)
	if err != nil {
		return errors.WithMessage(err, "failed get storage")
	}
	if storage.Disabled {
		return errors.Errorf("this storage have disabled")
	}
	storageDriver, err := GetStorageByMountPath(storage.MountPath)
	if err != nil {
		return errors.WithMessage(err, "failed get storage driver")
	}
	// drop the storage in the driver
	if err := storageDriver.Drop(ctx); err != nil {
		return errors.Wrap(err, "failed drop storage")
	}
	// delete the storage in the memory
	storage.Disabled = true
	storage.SetStatus(DISABLED)
	err = db.UpdateStorage(storage)
	if err != nil {
		return errors.WithMessage(err, "failed update storage in db")
	}
	storagesMap.Delete(storage.MountPath)
	go callStorageHooks("del", storageDriver)
	return nil
}

// UpdateStorage update storage
// get old storage first
// drop the storage then reinitialize
func UpdateStorage(ctx context.Context, storage model.Storage) error {
	oldStorage, err := db.GetStorageById(storage.ID)
	if err != nil {
		return errors.WithMessage(err, "failed get old storage")
	}
	if oldStorage.Driver != storage.Driver {
		return errors.Errorf("driver cannot be changed")
	}

	if storage.SyncGroup {
		storage.Modified = time.Now()
		storage.SyncGroup = false
		storage.MountPath = utils.FixAndCleanPath(storage.MountPath)

		// 对比新旧存储获取修改的部分对比字段为:order,cache_expiration,remark,group采取直接替换的办法，目前没有实现有需要的话再说
		// 目前只修改addition序列化对比然后替换相关字段
		var changeMap = make(map[string]interface{}) // 声明一个map用来记录变化数据
		var storageMap map[string]interface{}        // 使用一个空接口表示可以是任意类型
		storageAdditionStr := storage.Addition
		err := json.Unmarshal([]byte(storageAdditionStr), &storageMap)
		if err != nil {
			return errors.Errorf("反序列化新存储失败")
		}

		var oldStorageMap map[string]interface{} // 使用一个空接口表示可以是任意类型
		oldStorageAdditionStr := oldStorage.Addition
		err = json.Unmarshal([]byte(oldStorageAdditionStr), &oldStorageMap)
		if err != nil {
			return errors.Errorf("反序列化旧存储失败")
		}

		for key, value := range storageMap {
			oldValue := oldStorageMap[key]
			if oldValue != value {
				//changeMap[oldValue.(string)] = value.(string)
				changeMap[key] = value
			}
		}

		if len(changeMap) == 0 {
			return errors.Errorf("Addition信息未发生变化，如需修改请关闭同步组存储选项！！！")
		}

		update_err := db.UpdateGroupStorages(storage.Group, changeMap)
		if update_err != nil {
			return errors.WithMessage(err, "更新同组存储数据失败")
		}
		//同组Addition数据修改完毕

		// err = db.UpdateStorage(&storage)
		// if err != nil {
		// 	return errors.WithMessage(err, "failed update storage in database")
		// }
		if storage.Disabled {
			return nil
		}
		if oldStorage.MountPath != storage.MountPath {
			// mount path renamed, need to drop the storage
			storagesMap.Delete(oldStorage.MountPath)
		}

		storages, err := db.GetGroupStorages(storage.Group)
		go func(storages []model.Storage) {
			for _, storage := range storages {
				storageDriver, err := GetStorageByMountPath(storage.MountPath)
				if err != nil {
					log.Errorf("failed get storage driver: %+v", err)
					continue
				}
				// drop the storage in the driver
				if err := storageDriver.Drop(context.Background()); err != nil {
					log.Errorf("failed drop storage: %+v", err)
					continue
				}
				if err := LoadStorage(context.Background(), storage); err != nil {
					log.Errorf("failed get enabled storages: %+v", err)
					continue
				}
				log.Infof("success load storage: [%s], driver: [%s]",
					storage.MountPath, storage.Driver)
			}
			conf.StoragesLoaded = true
		}(storages)

		// storageDriver, err := GetStorageByMountPath(oldStorage.MountPath)
		// if err != nil {
		// 	return errors.WithMessage(err, "failed get storage driver")
		// }
		// err = storageDriver.Drop(ctx)
		// if err != nil {
		// 	return errors.Wrapf(err, "failed drop storage")
		// }

		// err = initStorage(ctx, storage, storageDriver)
		// go callStorageHooks("update", storageDriver)
		// log.Debugf("storage %+v is update", storageDriver)

		return err
	} else {
		storage.Modified = time.Now()
		storage.MountPath = utils.FixAndCleanPath(storage.MountPath)
		storage.SyncGroup = false
		err = db.UpdateStorage(&storage)
		if err != nil {
			return errors.WithMessage(err, "failed update storage in database")
		}
		if storage.Disabled {
			return nil
		}
		storageDriver, err := GetStorageByMountPath(oldStorage.MountPath)
		if oldStorage.MountPath != storage.MountPath {
			// mount path renamed, need to drop the storage
			storagesMap.Delete(oldStorage.MountPath)
		}
		if err != nil {
			return errors.WithMessage(err, "failed get storage driver")
		}
		err = storageDriver.Drop(ctx)
		if err != nil {
			return errors.Wrapf(err, "failed drop storage")
		}

		err = initStorage(ctx, storage, storageDriver)
		go callStorageHooks("update", storageDriver)
		log.Debugf("storage %+v is update", storageDriver)
		return err
	}

}

func DeleteStorageById(ctx context.Context, id uint) error {
	storage, err := db.GetStorageById(id)
	if err != nil {
		return errors.WithMessage(err, "failed get storage")
	}
	if !storage.Disabled {
		storageDriver, err := GetStorageByMountPath(storage.MountPath)
		if err != nil {
			return errors.WithMessage(err, "failed get storage driver")
		}
		// drop the storage in the driver
		if err := storageDriver.Drop(ctx); err != nil {
			return errors.Wrapf(err, "failed drop storage")
		}
		// delete the storage in the memory
		storagesMap.Delete(storage.MountPath)
		go callStorageHooks("del", storageDriver)
	}
	// delete the storage in the database
	if err := db.DeleteStorageById(id); err != nil {
		return errors.WithMessage(err, "failed delete storage in database")
	}
	return nil
}

func ForceDeleteStorageById(ctx context.Context, id uint) error {
	storage, err := db.GetStorageById(id)
	if err != nil {
		fmt.Println("failed get storage")
	}
	if !storage.Disabled {
		storageDriver, err := GetStorageByMountPath(storage.MountPath)
		if err != nil {
			return errors.WithMessage(err, "failed get storage driver")
		}
		// drop the storage in the driver
		if err := storageDriver.Drop(ctx); err != nil {
			return errors.Wrapf(err, "failed drop storage")
		}
		// delete the storage in the memory
		storagesMap.Delete(storage.MountPath)
		go callStorageHooks("del", storageDriver)
	}
	// delete the storage in the database
	if err := db.DeleteStorageById(id); err != nil {
		return errors.WithMessage(err, "failed delete storage in database")
	}
	return nil
}

// MustSaveDriverStorage call from specific driver
func MustSaveDriverStorage(driver driver.Driver) {
	err := saveDriverStorage(driver)
	if err != nil {
		log.Errorf("failed save driver storage: %s", err)
	}
}

func saveDriverStorage(driver driver.Driver) error {
	storage := driver.GetStorage()
	addition := driver.GetAddition()
	str, err := utils.Json.MarshalToString(addition)
	if err != nil {
		return errors.Wrap(err, "error while marshal addition")
	}
	storage.Addition = str
	err = db.UpdateStorage(storage)
	if err != nil {
		return errors.WithMessage(err, "failed update storage in database")
	}
	return nil
}

// getStoragesByPath get storage by longest match path, contains balance storage.
// for example, there is /a/b,/a/c,/a/d/e,/a/d/e.balance
// getStoragesByPath(/a/d/e/f) => /a/d/e,/a/d/e.balance
func getStoragesByPath(path string) []driver.Driver {
	storages := make([]driver.Driver, 0)
	curSlashCount := 0
	storagesMap.Range(func(mountPath string, value driver.Driver) bool {
		mountPath = utils.GetActualMountPath(mountPath)
		// is this path
		if utils.IsSubPath(mountPath, path) {
			slashCount := strings.Count(utils.PathAddSeparatorSuffix(mountPath), "/")
			// not the longest match
			if slashCount > curSlashCount {
				storages = storages[:0]
				curSlashCount = slashCount
			}
			if slashCount == curSlashCount {
				storages = append(storages, value)
			}
		}
		return true
	})
	// make sure the order is the same for same input
	sort.Slice(storages, func(i, j int) bool {
		return storages[i].GetStorage().MountPath < storages[j].GetStorage().MountPath
	})
	return storages
}

// GetStorageVirtualFilesByPath Obtain the virtual file generated by the storage according to the path
// for example, there are: /a/b,/a/c,/a/d/e,/a/b.balance1,/av
// GetStorageVirtualFilesByPath(/a) => b,c,d
func GetStorageVirtualFilesByPath(prefix string) []model.Obj {
	files := make([]model.Obj, 0)
	storages := storagesMap.Values()
	sort.Slice(storages, func(i, j int) bool {
		if storages[i].GetStorage().Order == storages[j].GetStorage().Order {
			return storages[i].GetStorage().MountPath < storages[j].GetStorage().MountPath
		}
		return storages[i].GetStorage().Order < storages[j].GetStorage().Order
	})

	prefix = utils.FixAndCleanPath(prefix)
	set := mapset.NewSet[string]()
	for _, v := range storages {
		mountPath := utils.GetActualMountPath(v.GetStorage().MountPath)
		// Exclude prefix itself and non prefix
		if len(prefix) >= len(mountPath) || !utils.IsSubPath(prefix, mountPath) {
			continue
		}
		name := strings.SplitN(strings.TrimPrefix(mountPath[len(prefix):], "/"), "/", 2)[0]
		if set.Add(name) {
			files = append(files, &model.Object{
				Name:     name,
				Size:     0,
				Modified: v.GetStorage().Modified,
				IsFolder: true,
			})
		}
	}
	return files
}

var balanceMap generic_sync.MapOf[string, int]

// GetBalancedStorage get storage by path
func GetBalancedStorage(path string) driver.Driver {
	path = utils.FixAndCleanPath(path)
	storages := getStoragesByPath(path)
	storageNum := len(storages)
	switch storageNum {
	case 0:
		return nil
	case 1:
		return storages[0]
	default:
		virtualPath := utils.GetActualMountPath(storages[0].GetStorage().MountPath)
		i, _ := balanceMap.LoadOrStore(virtualPath, 0)
		i = (i + 1) % storageNum
		balanceMap.Store(virtualPath, i)
		return storages[i]
	}
}
