package app

import (
	"encoding/json"
	"log"

	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	"github.com/cosmos/cosmos-sdk/x/staking"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	tmprototypes "github.com/tendermint/tendermint/proto/tendermint/types"
)

// ExportAppStateAndValidators exports the state of the application for a genesis
// file.
func (a *App) ExportAppStateAndValidators(
	forZeroHeight bool, jailAllowedAddrs []string,
) (servertypes.ExportedApp, error) { // as if they could withdraw from the start of the next block
	ctx := a.NewContext(true, tmprototypes.Header{Height: a.LastBlockHeight()})

	// We export at last height + 1, because that's the height at which
	// Tendermint will start InitChain.
	height := a.LastBlockHeight() + 1
	if forZeroHeight {
		height = 0
		a.prepForZeroHeightGenesis(ctx, jailAllowedAddrs)
	}

	genState := a.mm.ExportGenesis(ctx, a.cdc)
	appState, err := json.MarshalIndent(genState, "", "  ")
	if err != nil {
		return servertypes.ExportedApp{}, err
	}

	validators, err := staking.WriteValidators(ctx, a.StakingKeeper)
	if err != nil {
		return servertypes.ExportedApp{}, err
	}
	return servertypes.ExportedApp{
		AppState:        appState,
		Validators:      validators,
		Height:          height,
		ConsensusParams: a.BaseApp.GetConsensusParams(ctx),
	}, nil
}

// prepare for fresh start at zero height
// NOTE zero height genesis is a temporary feature which will be deprecated
//
//	in favour of export at a block height
func (a *App) prepForZeroHeightGenesis(ctx sdk.Context, jailAllowedAddrs []string) {
	applyAllowedAddrs := false

	// check if there is a allowed address list
	if len(jailAllowedAddrs) > 0 {
		applyAllowedAddrs = true
	}

	allowedAddrsMap := make(map[string]bool)

	for _, addr := range jailAllowedAddrs {
		_, err := sdk.ValAddressFromBech32(addr)
		if err != nil {
			log.Fatal(err)
		}
		allowedAddrsMap[addr] = true
	}

	/* Just to be safe, assert the invariants on current state. */
	a.CrisisKeeper.AssertInvariants(ctx)

	/* Handle fee distribution state. */

	// withdraw all validator commission
	a.StakingKeeper.IterateValidators(ctx, func(_ int64, val stakingtypes.ValidatorI) (stop bool) {
		_, err := a.DistrKeeper.WithdrawValidatorCommission(ctx, val.GetOperator())
		if err != nil {
			panic(err)
		}
		return false
	})

	// withdraw all delegator rewards
	dels := a.StakingKeeper.GetAllDelegations(ctx)
	for _, delegation := range dels {
		_, err := a.DistrKeeper.WithdrawDelegationRewards(ctx, delegation.GetDelegatorAddr(), delegation.GetValidatorAddr())
		if err != nil {
			panic(err)
		}
	}

	// clear validator slash events
	a.DistrKeeper.DeleteAllValidatorSlashEvents(ctx)

	// clear validator historical rewards
	a.DistrKeeper.DeleteAllValidatorHistoricalRewards(ctx)

	// set context height to zero
	height := ctx.BlockHeight()
	ctx = ctx.WithBlockHeight(0)

	// reinitialize all validators
	a.StakingKeeper.IterateValidators(ctx, func(_ int64, val stakingtypes.ValidatorI) (stop bool) {
		// donate any unwithdrawn outstanding reward fraction tokens to the community pool
		scraps := a.DistrKeeper.GetValidatorOutstandingRewardsCoins(ctx, val.GetOperator())
		feePool := a.DistrKeeper.GetFeePool(ctx)
		feePool.CommunityPool = feePool.CommunityPool.Add(scraps...)
		a.DistrKeeper.SetFeePool(ctx, feePool)

		a.DistrKeeper.Hooks().AfterValidatorCreated(ctx, val.GetOperator())
		return false
	})

	// reinitialize all delegations
	for _, del := range dels {
		a.DistrKeeper.Hooks().BeforeDelegationCreated(ctx, del.GetDelegatorAddr(), del.GetValidatorAddr())
		a.DistrKeeper.Hooks().AfterDelegationModified(ctx, del.GetDelegatorAddr(), del.GetValidatorAddr())
	}

	// reset context height
	ctx = ctx.WithBlockHeight(height)

	/* Handle staking state. */

	// iterate through redelegations, reset creation height
	a.StakingKeeper.IterateRedelegations(ctx, func(_ int64, red stakingtypes.Redelegation) (stop bool) {
		for i := range red.Entries {
			red.Entries[i].CreationHeight = 0
		}
		a.StakingKeeper.SetRedelegation(ctx, red)
		return false
	})

	// iterate through unbonding delegations, reset creation height
	a.StakingKeeper.IterateUnbondingDelegations(ctx, func(_ int64, ubd stakingtypes.UnbondingDelegation) (stop bool) {
		for i := range ubd.Entries {
			ubd.Entries[i].CreationHeight = 0
		}
		a.StakingKeeper.SetUnbondingDelegation(ctx, ubd)
		return false
	})

	// Iterate through validators by power descending, reset bond heights, and
	// update bond intra-tx counters.
	store := ctx.KVStore(a.keys[stakingtypes.StoreKey])
	iter := sdk.KVStoreReversePrefixIterator(store, stakingtypes.ValidatorsKey)
	counter := int16(0)

	for ; iter.Valid(); iter.Next() {
		addr := sdk.ValAddress(iter.Key()[1:])
		validator, found := a.StakingKeeper.GetValidator(ctx, addr)
		if !found {
			panic("expected validator, not found")
		}

		validator.UnbondingHeight = 0
		if applyAllowedAddrs && !allowedAddrsMap[addr.String()] {
			validator.Jailed = true
		}

		a.StakingKeeper.SetValidator(ctx, validator)
		counter++
	}

	err := iter.Close()
	if err != nil {
		return
	}

	if _, err := a.StakingKeeper.ApplyAndReturnValidatorSetUpdates(ctx); err != nil {
		panic(err)
	}

	/* Handle slashing state. */

	// reset start height on signing infos
	a.SlashingKeeper.IterateValidatorSigningInfos(
		ctx,
		func(addr sdk.ConsAddress, info slashingtypes.ValidatorSigningInfo) (stop bool) {
			info.StartHeight = 0
			a.SlashingKeeper.SetValidatorSigningInfo(ctx, addr, info)
			return false
		},
	)
}