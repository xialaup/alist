package db

import (
	"fmt"

	"github.com/alist-org/alist/v3/internal/model"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

// why don't need `cache` for storage?
// because all storage store in `op.storagesMap`
// the most of the read operation is from `op.storagesMap`
// just for persistence in database

// CreateStorage just insert storage to database
func CreateStorage(storage *model.Storage) error {
	return errors.WithStack(db.Create(storage).Error)
}

// UpdateStorage just update storage in database
func UpdateStorage(storage *model.Storage) error {
	return errors.WithStack(db.Save(storage).Error)
}

// DeleteStorageById just delete storage from database by id
func DeleteStorageById(id uint) error {
	return errors.WithStack(db.Delete(&model.Storage{}, id).Error)
}

// GetStorages Get all storages from database order by index
func GetStorages(pageIndex, pageSize int) ([]model.Storage, int64, error) {
	storageDB := db.Model(&model.Storage{})
	var count int64
	if err := storageDB.Count(&count).Error; err != nil {
		return nil, 0, errors.Wrapf(err, "failed get storages count")
	}
	var storages []model.Storage
	if err := storageDB.Order(columnName("order")).Offset((pageIndex - 1) * pageSize).Limit(pageSize).Find(&storages).Error; err != nil {
		return nil, 0, errors.WithStack(err)
	}
	return storages, count, nil
}

// GetStorageById Get Storage by id, used to update storage usually
func GetStorageById(id uint) (*model.Storage, error) {
	var storage model.Storage
	storage.ID = id
	if err := db.First(&storage).Error; err != nil {
		return nil, errors.WithStack(err)
	}
	return &storage, nil
}

// GetStorageByMountPath Get Storage by mountPath, used to update storage usually
func GetStorageByMountPath(mountPath string) (*model.Storage, error) {
	var storage model.Storage
	if err := db.Where("mount_path = ?", mountPath).First(&storage).Error; err != nil {
		return nil, errors.WithStack(err)
	}
	return &storage, nil
}

func GetEnabledStorages() ([]model.Storage, error) {
	var storages []model.Storage
	if err := db.Where(fmt.Sprintf("%s = ?", columnName("disabled")), false).Order(columnName("order")).Find(&storages).Error; err != nil {
		return nil, errors.WithStack(err)
	}
	return storages, nil
}

func GetGroupStorages(groupName string) ([]model.Storage, error) {
	var storages []model.Storage
	if err := db.Where(fmt.Sprintf("%s = ?", columnName("group")), groupName).Find(&storages).Error; err != nil {
		return nil, errors.WithStack(err)
	}
	return storages, nil
}

func UpdateGroupStorages(groupName string, changedAdditions map[string]interface{}) error {
	var storages []model.Storage
	if err := db.Where(fmt.Sprintf("%s = ?", columnName("group")), groupName).Find(&storages).Error; err != nil {
		return errors.WithStack(err)
	}
	// 动态构建 SQL 表达式
	ids := extractField(storages, func(u model.Storage) int { return int(u.ID) }) //提取同组存储的id组成数组

	//方案一：更新数据为字段名:新值类型
	expr := "addition"
	var args []interface{}
	for key, val := range changedAdditions {
		expr = fmt.Sprintf("JSON_SET(%s, ?, ?)", expr)
		args = append(args, "$."+key, val)
	}
	updates := map[string]interface{}{
		"addition": gorm.Expr(expr, args...),
	}
	// 执行更新
	if updateErr := db.Model(&model.Storage{}).Where("id IN ?", ids).Updates(updates).Error; updateErr != nil {
		return errors.WithStack(updateErr)
	}

	// 方案二 更新数据为旧数据：新数据类型
	// var keys []string
	// for oldStr := range changedAdditions {
	// 	keys = append(keys, oldStr)
	// }
	// sort.Strings(keys) // 按字典序排序
	// // 按排序后的键遍历
	// expr := "addition"
	// for _, oldStr := range keys {
	// 	newStr := changedAdditions[oldStr]
	// 	expr = fmt.Sprintf("REPLACE(%s, '%s', '%s')", expr, oldStr, newStr)
	// }
	// if updateErr := db.Model(&model.Storage{}).Where("id IN ?", ids).Update("addition", gorm.Expr(expr)).Error; updateErr != nil {
	// 	return errors.WithStack(updateErr)
	// }
	return nil
}

func extractField[T any, F any](slice []T, getter func(T) F) []F {
	result := make([]F, 0, len(slice))
	for _, item := range slice {
		result = append(result, getter(item))
	}
	return result
}
