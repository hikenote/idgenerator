package idgenerator

import (
	"code.byted.org/dfic/weili_lib/utils/random"
	"errors"
	"math"
	"strconv"
	"sync"
	"time"
)

const (
	CEpochMs              = 1480550400000 //20161201
	CEpochS               = 1480550400    //20161201
	DF_YMD                = "060102"
	DF_YMDHMS             = "060102150405"
	TimesimpleStyle       = 11
	SleepTime             = 600
	Decimal               = 10 //十进制格式
	Binary                = 2  //二进制格式
	CShift          int64 = 63
	LShift          int64 = 0
)

// IdWorker Struct
type IdWorker struct {
	gc            *GeneratorConfig
	workerId      int64
	workerIdLen   int64
	lastTimestamp int64
	timestampLen  int64
	sequence      int64
	sequenceLen   int64
	sequenceMask  int64
	reserved      int64
	reservedLen   int64
	userDef       int64
	userDefLen    int64
	randomId      int64
	randomIdLen   int64
	lock          *sync.Mutex
}

// NewIdWorker Func: Generate NewIdWorker with Given workerid
func NewIdWorker(generatorConfig *GeneratorConfig) *IdWorker {
	iw := new(IdWorker)
	iw.gc = generatorConfig
	for _, unit := range iw.gc.Unit {
		switch unit.FType {
		case "sequence":
			iw.sequence = unit.DefValue
			iw.sequenceLen = unit.Length
			iw.sequenceMask = getSequenceMask(iw.gc.GType, unit.Length)
		case "timestamp":
			iw.lastTimestamp = unit.DefValue
			iw.timestampLen = unit.Length
		case "reserved":
			iw.reserved = unit.DefValue
			iw.reservedLen = unit.Length
		case "workerid":
			iw.workerId = unit.DefValue
			iw.workerIdLen = unit.Length
		case "userdef":
			iw.userDef = unit.DefValue
			iw.userDefLen = unit.Length
		case "randomid":
			iw.randomId = unit.DefValue
			iw.randomIdLen = unit.Length
		}
	}
	iw.lock = new(sync.Mutex)
	return iw
}

func getSequenceMask(gtype int8, sequenceLen int64) int64 {
	if gtype == Decimal {
		return int64(math.Pow(10, float64(sequenceLen)))
	} else {
		return -1 ^ -1<<uint(sequenceLen)
	}
}

func formatString(number int64, size int) string {
	numberString := strconv.FormatInt(number, 10)
	if len(numberString) < size {
		leftSize := size - len(numberString)
		for leftSize > 0 {
			numberString = "0" + numberString
			leftSize--
		}
	}
	return numberString
}

func getRandomId(gtype int8, length int64) int64 {
	if gtype == Decimal {
		return random.Rndn64(int64(math.Pow(10, float64(length)) - 1))
	} else {
		return random.Rndn64(-1 ^ -1<<uint(length))
	}
}

func (iw *IdWorker) SetWorkerId(workerid int64) {
	iw.workerId = workerid
}

// return in ms
func (iw *IdWorker) timeGen(length int64) int64 {
	if length > 40 {
		return time.Now().UnixNano() / 1000 / 1000
	} else {
		return time.Now().UnixNano() / 1000 / 1000 / 1000
	}

}

func (iw *IdWorker) timeReGen(last int64) int64 {
	ts := iw.timeGen(iw.timestampLen)
	for {
		if ts <= last {
			ts = iw.timeGen(iw.timestampLen)
		} else {
			break
		}
	}
	return ts
}

// return in ts
func (iw *IdWorker) timePrefixGen(length int64) (ts int64) {
	tn := time.Now()
	if length == TimesimpleStyle {
		ts, _ = strconv.ParseInt(tn.Format(DF_YMD)+formatString(int64(tn.Hour()*3600+tn.Minute()*60+tn.Second()), 5), 10, 64)
	} else {
		ts, _ = strconv.ParseInt(tn.Format(DF_YMDHMS), 10, 64)
	}
	return
}

func (iw *IdWorker) timePrefixReGen(last int64) (ts int64) {
	ts = iw.timePrefixGen(iw.timestampLen)
	for {
		if ts <= last {
			ts = iw.timePrefixGen(iw.timestampLen)
		} else {
			break
		}
	}
	return
}

// NewId Func: Generate next id
func (iw *IdWorker) NextId(userdef ...int64) (generatorId int64, err error) {
	if len(userdef) > 0 {
		iw.userDef = userdef[0]
	}
	iw.lock.Lock()
	defer iw.lock.Unlock()
	if iw.gc.GType == Decimal {
		return iw.decimalFormat()
	} else {
		return iw.binaryFormat()
	}
}

func (iw *IdWorker) decimalFormat() (generatorId int64, err error) {
	ts := iw.timePrefixGen(iw.timestampLen)
	if ts == iw.lastTimestamp {
		if iw.sequence+1 >= iw.sequenceMask {
			time.Sleep(SleepTime * time.Millisecond) //sleep 500ms
			ts = iw.timePrefixReGen(ts)
			iw.sequence = 0
		} else {
			iw.sequence += 1
		}
	} else {
		iw.sequence = 0
	}
	if ts < iw.lastTimestamp {
		err = errors.New("Clock moved backwards, Refuse gen id")
		return 0, err
	}
	iw.lastTimestamp = ts

	randomIdStr := ""
	if iw.randomIdLen > 0 {
		iw.randomId = getRandomId(iw.gc.GType, iw.randomIdLen)
		randomIdStr = formatString(iw.randomId, int(iw.randomIdLen))
	}
	generatorStr := ""
	timestampStr := strconv.FormatInt(ts, 10)
	reservedStr := ""
	if iw.reservedLen > 0 {
		reservedStr = formatString(iw.reserved, int(iw.reservedLen))
	}
	userDefStr := ""
	if iw.userDefLen > 0 {
		userDefStr = formatString(iw.userDef, int(iw.userDefLen))
	}
	workerIdStr := formatString(iw.workerId, int(iw.workerIdLen))
	sequenceStr := formatString(iw.sequence, int(iw.sequenceLen))
	for _, unit := range iw.gc.Unit {
		switch unit.FType {
		case "sequence":
			generatorStr += sequenceStr
		case "timestamp":
			generatorStr += timestampStr
		case "reserved":
			generatorStr += reservedStr
		case "workerid":
			generatorStr += workerIdStr
		case "userdef":
			generatorStr += userDefStr
		case "randomid":
			generatorStr += randomIdStr
		}
	}
	if len(generatorStr) > 19 {
		err = errors.New("generator id failed")
		return 0, err
	}
	generatorId, _ = strconv.ParseInt(generatorStr, 10, 64)
	return generatorId, nil
}

func (iw *IdWorker) binaryFormat() (generatorId int64, err error) {
	ts := iw.timeGen(iw.timestampLen)
	if ts == iw.lastTimestamp {
		iw.sequence = (iw.sequence + 1) & iw.sequenceMask
		if iw.sequence == 0 {
			if iw.timestampLen > 40 {
				time.Sleep(time.Millisecond) //sleep 1ms
			} else {
				time.Sleep(SleepTime * time.Millisecond) //sleep 600ms
			}
			ts = iw.timeReGen(ts)
		}
	} else {
		iw.sequence = 0
	}

	if ts < iw.lastTimestamp {
		err = errors.New("Clock moved backwards, Refuse gen id")
		return 0, err
	}
	iw.lastTimestamp = ts
	if iw.timestampLen > 40 {
		ts -= CEpochMs
	} else {
		ts -= CEpochS
	}
	if iw.randomIdLen > 0 {
		iw.randomId = getRandomId(iw.gc.GType, iw.randomIdLen)
	}
	cShift := CShift
	for _, unit := range iw.gc.Unit {
		switch unit.FType {
		case "sequence":
			cShift -= iw.sequenceLen
			generatorId = generatorId | iw.sequence<<uint(cShift)
		case "timestamp":
			cShift -= iw.timestampLen
			generatorId = generatorId | ts<<uint(cShift)
		case "reserved":
			cShift -= iw.reservedLen
			generatorId = generatorId | iw.reserved<<uint(cShift)
		case "workerid":
			cShift -= iw.workerIdLen
			generatorId = generatorId | iw.workerId<<uint(cShift)
		case "userdef":
			cShift -= iw.userDefLen
			generatorId = generatorId | iw.userDef<<uint(cShift)
		case "randomid":
			cShift -= iw.randomIdLen
			generatorId = generatorId | iw.randomId<<uint(cShift)
		}
		if cShift < 0 {
			err = errors.New("the totalbit is not invalid")
			return 0, err
		}
	}
	return generatorId, nil
}

// ParseId Func: reverse uid to timestamp, workid, seq
func (iw *IdWorker) ParseId(id int64) (parseArr []int64, err error) {
	idStr := strconv.FormatInt(id, 10)
	if len(idStr) > 19 {
		err = errors.New("id is invalid")
		return parseArr, err
	}
	lShift := LShift
	cShift := CShift
	parseArr = make([]int64, len(iw.gc.Unit))
	for i, unit := range iw.gc.Unit {
		switch unit.FType {
		case "sequence":
			if iw.gc.GType == Decimal {
				parseArr[i], _ = strconv.ParseInt(idStr[lShift:lShift+iw.sequenceLen], 10, 64)
				lShift += iw.sequenceLen
			} else {
				cShift -= iw.sequenceLen
				parseArr[i] = id >> uint(cShift) & (-1 ^ -1<<uint(iw.sequenceLen))
			}
		case "timestamp":
			if iw.gc.GType == Decimal {
				parseArr[i], _ = strconv.ParseInt(idStr[lShift:lShift+iw.timestampLen], 10, 64)
				lShift += iw.timestampLen
			} else {
				cShift -= iw.timestampLen
				if iw.timestampLen > 40 {
					parseArr[i] = id>>uint(cShift)&(-1^-1<<uint(iw.timestampLen)) + CEpochMs
				} else {
					parseArr[i] = id>>uint(cShift)&(-1^-1<<uint(iw.timestampLen)) + CEpochS
				}
			}
		case "reserved":
			if iw.gc.GType == Decimal {
				parseArr[i], _ = strconv.ParseInt(idStr[lShift:lShift+iw.reservedLen], 10, 64)
				lShift += iw.reservedLen
			} else {
				cShift -= iw.reservedLen
				parseArr[i] = id >> uint(cShift) & (-1 ^ -1<<uint(iw.reservedLen))
			}
		case "workerid":
			if iw.gc.GType == Decimal {
				parseArr[i], _ = strconv.ParseInt(idStr[lShift:lShift+iw.workerIdLen], 10, 64)
				lShift += iw.workerIdLen
			} else {
				cShift -= iw.workerIdLen
				parseArr[i] = id >> uint(cShift) & (-1 ^ -1<<uint(iw.workerIdLen))
			}
		case "userdef":
			if iw.gc.GType == Decimal {
				parseArr[i], _ = strconv.ParseInt(idStr[lShift:lShift+iw.userDefLen], 10, 64)
				lShift += iw.userDefLen
			} else {
				cShift -= iw.userDefLen
				parseArr[i] = id >> uint(cShift) & (-1 ^ -1<<uint(iw.userDefLen))
			}
		case "randomid":
			if iw.gc.GType == Decimal {
				parseArr[i], _ = strconv.ParseInt(idStr[lShift:lShift+iw.randomIdLen], 10, 64)
				lShift += iw.randomIdLen
			} else {
				cShift -= iw.randomIdLen
				parseArr[i] = id >> uint(cShift) & (-1 ^ -1<<uint(iw.randomIdLen))
			}
		}
	}
	return parseArr, nil
}
