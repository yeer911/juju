// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package storage

import (
	"github.com/juju/errors"
	"github.com/juju/names"
	"github.com/juju/utils/set"

	"github.com/juju/juju/apiserver/common"
	"github.com/juju/juju/apiserver/params"
	"github.com/juju/juju/feature"
	"github.com/juju/juju/state"
	"github.com/juju/juju/storage"
	"github.com/juju/juju/storage/poolmanager"
	"github.com/juju/juju/storage/provider/registry"
)

func init() {
	common.RegisterStandardFacadeForFeature("Storage", 1, NewAPI, feature.Storage)
}

// API implements the storage interface and is the concrete
// implementation of the api end point.
type API struct {
	storage     storageAccess
	poolManager poolmanager.PoolManager
	authorizer  common.Authorizer
}

// createAPI returns a new storage API facade.
func createAPI(
	st storageAccess,
	pm poolmanager.PoolManager,
	resources *common.Resources,
	authorizer common.Authorizer,
) (*API, error) {
	if !authorizer.AuthClient() {
		return nil, common.ErrPerm
	}

	return &API{
		storage:     st,
		poolManager: pm,
		authorizer:  authorizer,
	}, nil
}

// NewAPI returns a new storage API facade.
func NewAPI(
	st *state.State,
	resources *common.Resources,
	authorizer common.Authorizer,
) (*API, error) {
	return createAPI(getState(st), poolManager(st), resources, authorizer)
}

func poolManager(st *state.State) poolmanager.PoolManager {
	return poolmanager.New(state.NewStateSettings(st))
}

// Show retrieves and returns detailed information about desired storage
// identified by supplied tags. If specified storage cannot be retrieved,
// individual error is returned instead of storage information.
func (api *API) Show(entities params.Entities) (params.StorageDetailsResults, error) {
	var all []params.StorageDetailsResult
	for _, entity := range entities.Entities {
		storageTag, err := names.ParseStorageTag(entity.Tag)
		if err != nil {
			all = append(all, params.StorageDetailsResult{
				Error: common.ServerError(err),
			})
			continue
		}
		found, instance, serverErr := api.getStorageInstance(storageTag)
		if err != nil {
			all = append(all, params.StorageDetailsResult{Error: serverErr})
			continue
		}
		if found {
			results := api.createStorageDetailsResult(storageTag, instance)
			all = append(all, results...)
		}
	}
	return params.StorageDetailsResults{Results: all}, nil
}

// List returns all currently known storage. Unlike Show(),
// if errors encountered while retrieving a particular
// storage, this error is treated as part of the returned storage detail.
func (api *API) List() (params.StorageInfosResult, error) {
	stateInstances, err := api.storage.AllStorageInstances()
	if err != nil {
		return params.StorageInfosResult{}, common.ServerError(err)
	}
	var infos []params.StorageInfo
	for _, stateInstance := range stateInstances {
		storageTag := stateInstance.StorageTag()
		persistent, err := api.isPersistent(storageTag)
		if err != nil {
			return params.StorageInfosResult{}, err
		}
		instance := createParamsStorageInstance(stateInstance, persistent)

		// It is possible to encounter errors here related to getting individual
		// storage details such as getting attachments, getting machine from the unit,
		// etc.
		// Current approach is to do what status command does - treat error
		// as another valid property, i.e. augment storage details.
		attachments := api.createStorageDetailsResult(storageTag, instance)
		for _, one := range attachments {
			aParam := params.StorageInfo{one.Result, one.Error}
			infos = append(infos, aParam)
		}
	}
	return params.StorageInfosResult{Results: infos}, nil
}

func (api *API) createStorageDetailsResult(
	storageTag names.StorageTag,
	instance params.StorageDetails,
) []params.StorageDetailsResult {
	attachments, err := api.getStorageAttachments(storageTag, instance)
	if err != nil {
		return []params.StorageDetailsResult{params.StorageDetailsResult{Result: instance, Error: err}}
	}
	if len(attachments) > 0 {
		// If any attachments were found for this storage instance,
		// return them instead.
		result := make([]params.StorageDetailsResult, len(attachments))
		for i, attachment := range attachments {
			result[i] = params.StorageDetailsResult{Result: attachment}
		}
		return result
	}
	// If we are here then this storage instance is unattached.
	return []params.StorageDetailsResult{params.StorageDetailsResult{Result: instance}}
}

func (api *API) getStorageAttachments(
	storageTag names.StorageTag,
	instance params.StorageDetails,
) ([]params.StorageDetails, *params.Error) {
	serverError := func(err error) *params.Error {
		return common.ServerError(errors.Annotatef(err, "getting attachments for storage %v", storageTag.Id()))
	}
	stateAttachments, err := api.storage.StorageAttachments(storageTag)
	if err != nil {
		return nil, serverError(common.ErrPerm)
	}
	result := make([]params.StorageDetails, len(stateAttachments))
	for i, one := range stateAttachments {
		paramsStorageAttachment, err := api.createParamsStorageAttachment(instance, one)
		if err != nil {
			return nil, serverError(err)
		}
		result[i] = paramsStorageAttachment
	}
	return result, nil
}

func (api *API) createParamsStorageAttachment(si params.StorageDetails, sa state.StorageAttachment) (params.StorageDetails, error) {
	result := params.StorageDetails{Status: "pending"}
	result.StorageTag = sa.StorageInstance().String()
	if result.StorageTag != si.StorageTag {
		panic("attachment does not belong to storage instance")
	}
	result.UnitTag = sa.Unit().String()
	result.OwnerTag = si.OwnerTag
	result.Kind = si.Kind
	result.Persistent = si.Persistent
	// TODO(axw) set status according to whether storage has been provisioned.

	// This is only for provisioned attachments
	machineTag, err := api.storage.UnitAssignedMachine(sa.Unit())
	if err != nil {
		return params.StorageDetails{}, errors.Annotate(err, "getting unit for storage attachment")
	}
	info, err := common.StorageAttachmentInfo(api.storage, sa, machineTag)
	if err != nil {
		if errors.IsNotProvisioned(err) {
			// If Info returns an error, then the storage has not yet been provisioned.
			return result, nil
		}
		return params.StorageDetails{}, errors.Annotate(err, "getting storage attachment info")
	}
	result.Location = info.Location
	if result.Location != "" {
		result.Status = "attached"
	}
	return result, nil
}

func (api *API) getStorageInstance(tag names.StorageTag) (bool, params.StorageDetails, *params.Error) {
	nothing := params.StorageDetails{}
	serverError := func(err error) *params.Error {
		return common.ServerError(errors.Annotatef(err, "getting %v", tag))
	}
	stateInstance, err := api.storage.StorageInstance(tag)
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nothing, nil
		}
		return false, nothing, serverError(common.ErrPerm)
	}
	persistent, err := api.isPersistent(tag)
	if err != nil {
		return false, nothing, serverError(err)
	}
	return true, createParamsStorageInstance(stateInstance, persistent), nil
}

func createParamsStorageInstance(si state.StorageInstance, persistent bool) params.StorageDetails {
	result := params.StorageDetails{
		OwnerTag:   si.Owner().String(),
		StorageTag: si.Tag().String(),
		Kind:       params.StorageKind(si.Kind()),
		Status:     "pending",
		Persistent: persistent,
	}
	return result
}

func (api *API) isPersistent(tag names.StorageTag) (bool, error) {
	volume, err := api.storage.StorageInstanceVolume(tag)
	if err != nil {
		return false, common.ErrPerm
	}
	// If the volume is not provisioned, we read its config attributes.
	if params, ok := volume.Params(); ok {
		_, cfg, err := common.StoragePoolConfig(params.Pool, api.poolManager)
		if err != nil {
			return false, err
		}
		return cfg.IsPersistent(), nil
	}
	// If the volume is provisioned, we look at its provisioning info.
	info, err := volume.Info()
	if err != nil {
		return false, err
	}
	return info.Persistent, nil
}

// ListPools returns a list of pools.
// If filter is provided, returned list only contains pools that match
// the filter.
// Pools can be filtered on names and provider types.
// If both names and types are provided as filter,
// pools that match either are returned.
// If no filter is provided, all pools are returned.
func (a *API) ListPools(
	filter params.StoragePoolFilter,
) (params.StoragePoolsResult, error) {

	all, err := a.poolManager.List()
	if err != nil {
		return params.StoragePoolsResult{}, err
	}
	results := []params.StoragePool{}
	if ok, err := a.isValidPoolListFilter(filter); !ok {
		return params.StoragePoolsResult{}, err
	}
	// Convert to sets as easier to deal with
	providerSet := set.NewStrings(filter.Providers...)
	nameSet := set.NewStrings(filter.Names...)
	for _, apool := range all {
		if poolMatchesFilters(apool, providerSet, nameSet) {
			results = append(results,
				params.StoragePool{
					Name:     apool.Name(),
					Provider: string(apool.Provider()),
					Attrs:    apool.Attrs(),
				})
		}
	}
	return params.StoragePoolsResult{Results: results}, nil
}

func (a *API) isValidPoolListFilter(
	filter params.StoragePoolFilter,
) (bool, error) {
	if len(filter.Providers) != 0 {
		if valid, err := a.isValidProviderCriteria(filter.Providers); !valid {
			return false, errors.Trace(err)
		}
	}
	if len(filter.Names) != 0 {
		if valid, err := a.isValidNameCriteria(filter.Names); !valid {
			return false, errors.Trace(err)
		}
	}
	return true, nil
}

func (a *API) isValidNameCriteria(names []string) (bool, error) {
	for _, n := range names {
		if !storage.IsValidPoolName(n) {
			return false, errors.NotValidf("pool name %q", n)
		}
	}
	return true, nil
}

func (a *API) isValidProviderCriteria(providers []string) (bool, error) {
	envName, err := a.storage.EnvName()
	if err != nil {
		return false, errors.Annotate(err, "getting env name")
	}
	for _, p := range providers {
		if !registry.IsProviderSupported(envName, storage.ProviderType(p)) {
			return false, errors.NotSupportedf("%q for environment %q", p, envName)
		}
	}
	return true, nil
}

func poolMatchesFilters(
	apool *storage.Config,
	providerFilter,
	nameFilter set.Strings,
) bool {
	// no filters supplied = pool matches criteria
	if providerFilter.IsEmpty() && nameFilter.IsEmpty() {
		return true
	}

	// if at least 1 name and type are supplied, use AND to match
	if !providerFilter.IsEmpty() && !nameFilter.IsEmpty() {
		return nameFilter.Contains(apool.Name()) &&
			providerFilter.Contains(string(apool.Provider()))
	}
	// Otherwise, if only names or types are supplied, use OR to match
	return nameFilter.Contains(apool.Name()) ||
		providerFilter.Contains(string(apool.Provider()))
}

// CreatePool creates a new pool with specified parameters.
func (a *API) CreatePool(p params.StoragePool) error {
	_, err := a.poolManager.Create(
		p.Name,
		storage.ProviderType(p.Provider),
		p.Attrs)
	return err
}
