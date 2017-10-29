### 前言：
      唯一全局ID是我们在设计一个系统的时候常常会遇见的问题，也常常为这个问题而纠结。生成ID的方法有很多，适应不同的场景、需求以及性能要求。所以有些比较复杂的系统会有多个ID生成的策略。
### 常见的id生成策略：

1. 数据库自增长字段
                优点：简单、性能良好，对分页及排序非常方便
                缺点：完全自增易造成数据泄露，迁移麻烦，单库的时候易造成单点，多库维护较麻烦
2. UUID
                优点：简单、性能有保证，全球唯一
                缺点：无序无法实现趋势递增，字符串存储查询效率较低，不可读
3. redis自增
                借助redis单线程通过incr、incrby实现唯一自增id
                优点：速度比数据库自增快，对分页及排序友好
                缺点：引入redis增加一定维护成本
4. mongodb的objectid（目前图虫社区在用） 
                实现方式与snowflake类似，非常轻量级，不同机器能采用全局唯一id生成。
                优点：分布式全局唯一id
                缺点：需要维护mongodb成本
5. snowflake 
                snowflake是Twitter开源的分布式ID生成算法，结果是一个long型的ID。其核心思想是：使用41bit作为毫秒数，10bit作为机器的ID（5个bit是数据中心，5个bit的机器ID），12bit作为毫秒内的流水号（意味着每个节点在每毫秒可以产生 4096 个 ID），最后还有一个符号位，永远是0。
                优点：分布式全局唯一id，灵活方便，不依赖具体储存工具、趋势递增
                缺点：实现成本更高、时钟有可能出现不同步
### idgenerator:
####需求：
生成一个全局唯一的64整型id，图虫创意这块主要有两块地方：

* 图片 由于需要存储过亿的图片，每个图片需要有一个唯一的全局id，同时需要防止被外部发现我们的图片总量，通过有规律的id大量抓取图片，id必须满足趋势递增原则；
* 订单 业务订单对于我们的非常重要，为防止对手分析，id不能严格递增，同时又需要满足一定规则即可读（主要是十进制模式）。因为系统是分布式架构，所以id生成器必须支持分布式要求。
####概要：
针对业务的需求，我们采用了snowflake的思想进行改造id生成器，设计了一个idgenerator，idgenerator主要的设计目标：保证全局唯一、趋势递增、分段可配、可读、支持分布式、性能有保证，实施分段策略，提供反解析功能。
####实现：
idgenerator主要分为6段

* reserved 保留位  待使用段，默认占1位
* userdef 用户自定义，默认占1~2位
* timestamp 时间戳，支持常规时间戳（1480550400），默认占30位；字符时间格式（17102012151），默认占11位
* wordid 机器id，每台机器有一个全局唯一id，默认支持64台，占6位
* sequence 递增序列，在单个实例同一时间点默认支持8192个并发
* randomid 随机数值，主要是分表使用，实现数据均匀分布，默认占4位
以上6段都是根据图虫创意目前的业务需求设计的，每个段都可配，也可以自由组合。
            
idgenerator支持了2种id生成模式分别是二进制模式binary、十进制模式decimal。这里特别说明下由于服务部署在tce（docker容器）上，每个实例都有全局唯一的MY_POD_NAME，因此需要将该字符串映射成唯一workerId，以下是各个模块的示例代码片段。

生成workId：
```
//workerName即MY_POD_NAME
//生成唯一workerId
func GetWorkerId(workerName string, redis *redis.RedisManager) (workerId int64, err error) {
	signKey := workerName
	is_exists, err := redis.HExists("worker_id", signKey)
	if err != nil {
		return
	}
	if !is_exists {
		workerIdStr, err1 := redis.Hvals("worker_id")
		if err1 != nil {
			err = err1
			return
		}
		workerIds := make([]int64, 0)
		for _, workerStr := range workerIdStr {
			workerId := utils.MustInt64(workerStr)
			workerIds = append(workerIds, workerId)
		}
		workerId = random.Rndn64(63)
		len := 1
		for len < 64 && utils.FindIntInt64Slice(workerId, workerIds) {
			workerId++
			if workerId > 63 {
				workerId = 0
			}
			len++
		}
		if len >= 64 {
			err2 := fmt.Errorf("the worker id is full")
			err = err2
			return
		}
		redis.Hset("worker_id", signKey, workerId)
		redis.Hset("worker_timestamp", signKey, time.Now().Add(time.Second*1800).Unix())
	} else {
		workerId, err = redis.GetHInt64("worker_id", signKey)
		if err != nil {
			return
		}
	}
	return
}


//刷新workerId
func RefreshWorkerId(workerName string, redis *redis.RedisManager) {
	signKey := workerName
	ticker := time.NewTicker(time.Duration(1800) * time.Second)
	for {
		select {
		case <-ticker.C:
			curr := time.Now().Unix()
			workerMap, err := redis.HgetAll("worker_timestamp")
			if err == nil {
				for key, timestamp := range workerMap {
					if key != signKey {
						timestampInt64 := utils.MustInt64(timestamp)
						if curr-timestampInt64 > 21600 { //6个小时
							redis.HDel("worker_timestamp", key)
							redis.HDel("worker_id", key)
						}
					}
				}
			}
			redis.Hset("worker_timestamp", signKey, time.Now().Add(time.Second*1800).Unix())
		}
	}
}
```
代码说明：代码借助了redis存储每个workerName→workerId的映射，保证每个实例不出现重复workerId，由于业务升级会销毁实例，所以增加了定时刷新的逻辑，将过期的workerId下线，现阶段业务目前最大到64个实例同时在线提供id生成服务。

idgenerator初始化：
```
//定义配置结构体及默认值
var (
	Reserved  = map[string]int64{"DefValue": 0, "Value": 0, "Length": 1}
	Timestamp = map[string]int64{"DefValue": -1, "Value": 0, "Length": 11}
	UserDef   = map[string]int64{"DefValue": 0, "Value": 0, "Length": 0}
	WorkerId  = map[string]int64{"DefValue": 0, "Value": 0, "Length": 2}
	Sequence  = map[string]int64{"DefValue": 0, "Value": 0, "Length": 0}
	RandomId  = map[string]int64{"DefValue": 0, "Value": 0, "Length": 4}
)

type GeneratorConfig struct {
	GType int8
	Unit  []*FormatUnit
}
type FormatUnit struct {
	FType    string // timestamp, reserved, userdef, workerid, sequence, randomid
	DefValue int64
	Value    int64
	Length   int64
}


//生成idgenerator实例方法
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
//具体实例化代码
idgeneratorDecimal = idgenerator.NewIdWorker(&idgenerator.GeneratorConfig{
		GType: idgenerator.Decimal,  //十进制模式
		Unit: []*idgenerator.FormatUnit{
			&idgenerator.FormatUnit{
				FType:    "userdef",
				DefValue: 6,
				Length:   1,
			},
			&idgenerator.FormatUnit{
				FType:    "reserved",
				DefValue: 0,
				Length:   1,
			},
			&idgenerator.FormatUnit{
				FType:    "timestamp",
				DefValue: idgenerator.Timestamp["DefValue"],
				Length:   idgenerator.Timestamp["Length"],
			},
			&idgenerator.FormatUnit{
				FType:    "workerid",
				DefValue: idgenerator.WorkerId["DefValue"],
				Length:   idgenerator.WorkerId["Length"],
			},
			&idgenerator.FormatUnit{
				FType:    "sequence",
				DefValue: idgenerator.Sequence["DefValue"],
				Length:   4,
			},
		},
	})
idgeneratorBinary = idgenerator.NewIdWorker(&idgenerator.GeneratorConfig{
		GType: idgenerator.Binary,
		Unit: []*idgenerator.FormatUnit{
			&idgenerator.FormatUnit{
				FType:    "timestamp",
				DefValue: idgenerator.Timestamp["DefValue"],
				Length:   30,
			},
			&idgenerator.FormatUnit{
				FType:    "reserved",
				DefValue: 0,
				Length:   2,
			},
			&idgenerator.FormatUnit{
				FType:    "userdef",
				DefValue: idgenerator.UserDef["DefValue"],
				Length:   8,
			},
			&idgenerator.FormatUnit{
				FType:    "workerid",
				DefValue: idgenerator.WorkerId["DefValue"],
				Length:   6,
			},
			&idgenerator.FormatUnit{
				FType:    "sequence",
				DefValue: idgenerator.Sequence["DefValue"],
				Length:   13,
			},
			&idgenerator.FormatUnit{
				FType:    "randomid",
				DefValue: idgenerator.RandomId["DefValue"],
				Length:   idgenerator.RandomId["Length"],
			},
		},
	})
```	
代码说明：最上面定义了GeneratorConfig结构体，及默认值，下面的实例定义了模式、每段的长度、配置及顺序组合。

idgenerator生成id：
```
// 生成唯一id，支持传入自定义数值，存放到userdef段
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
//十进制模式
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
// 生成时间段（字符串格式）
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
func (iw *IdWorker) timePrefixGen(length int64) (ts int64) {
	tn := time.Now()
	if length == TimesimpleStyle {
		ts, _ = strconv.ParseInt(tn.Format(DF_YMD)+formatString(int64(tn.Hour()*3600+tn.Minute()*60+tn.Second()), 5), 10, 64)
	} else {
		ts, _ = strconv.ParseInt(tn.Format(DF_YMDHMS), 10, 64)
	}
	return
}
//拼接字符串
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


//二进制模式
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
// 生成时间段（时间戳格式）
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
```
代码说明：idgenerator生成模式主要还是各个段的拼接，通过时间段及sequence控制保证趋势递增及唯一性，其中十进制模式通过字符串拼接方式、二进制模式通过位移方式生成唯一id。

idgenerator反解析id：
```
//反解析各个段
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
```
代码说明：主要包含两个方式，十进制模式通过截取定义好的每个段，二进制模式通过位移截取定义好的每个段。

### 总结：
idgenerator生成器主要是分段配置，定制化，比较灵活，满足了上面提到的需求，主要是图虫创意业务在使用，运行良好。
如有错误，欢迎指正，谢谢！
