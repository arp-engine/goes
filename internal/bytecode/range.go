package bytecode

func PackRangeTargets(keySlot, valueSlot int) int64 {
	return int64(uint32(int32(keySlot)))<<32 | int64(uint32(int32(valueSlot)))
}

func UnpackRangeTargets(packed int64) (int, int) {
	return int(int32(uint32(uint64(packed) >> 32))), int(int32(uint32(packed)))
}
