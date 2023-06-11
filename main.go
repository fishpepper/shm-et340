package main

//
// speedwire decoder inspired by
// https://github.com/snaptec/openWB/blob/master/packages/modules/sma_shm/speedwiredecoder.py
//

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/dmichael/go-multicast/multicast"
	"github.com/godbus/dbus/introspect"
	"github.com/godbus/dbus/v5"
	log "github.com/sirupsen/logrus"
)

const (
	address = "239.12.255.254:9522"
)

var conn, err = dbus.SystemBus()

var dbusConnected bool = false

type singlePhase struct {
	voltage float32 // Volts: 230,0
	a       float32 // Amps: 8,3
	power   float32 // Watts: 1909
	forward float64 // kWh, purchased power
	reverse float64 // kWh, sold power
}

const intro = `
<node>
   <interface name="com.victronenergy.BusItem">
    <signal name="PropertiesChanged">
      <arg type="a{sv}" name="properties" />
    </signal>
    <method name="SetValue">
      <arg direction="in"  type="v" name="value" />
      <arg direction="out" type="i" />
    </method>
    <method name="GetText">
      <arg direction="out" type="s" />
    </method>
    <method name="GetValue">
      <arg direction="out" type="v" />
    </method>
	</interface>` + introspect.IntrospectDataString + `</node> `

type objectpath string

var victronValues = map[int]map[objectpath]dbus.Variant{
	// 0: This will be used to store the VALUE variant
	0: map[objectpath]dbus.Variant{},
	// 1: This will be used to store the STRING variant
	1: map[objectpath]dbus.Variant{},
}

func (f objectpath) GetValue() (dbus.Variant, *dbus.Error) {
	var result dbus.Variant
	if f == "/" {
		// see https://github.com/mitchese/shm-et340/issues/9
		// even though we initially did not expost '/'
		// venus dbus2mqtt.py requested it anyways, resulting
		// in an error/crashing. However returning some "random" data here
		// seems to be fine with dbus2mqtt, though
		result = dbus.MakeVariant(0)
	} else {
		result = victronValues[0][f]
	}
	log.Debugf("GetValue() called for %s. Returning %s", f, result)
	return result, nil
}

func (f objectpath) GetText() (string, *dbus.Error) {
	var result string
	if f == "/" {
		// see GetValue() for '/'
		result = ""
	} else {
		// Why does this end up ""SOMEVAL"" ... trim it I guess
		result = strings.Trim(victronValues[1][f].String(), "\"")
	}
	log.Debugf("GetText() called for %s. Returning %s", f, result)
	return result, nil
}

func init() {
	lvl, ok := os.LookupEnv("LOG_LEVEL")
	if !ok {
		lvl = "info"
	}

	ll, err := log.ParseLevel(lvl)
	if err != nil {
		ll = log.DebugLevel
	}

	log.SetLevel(ll)
}

func initVictronValues() {
	// Need to implement following paths:
	// https://github.com/victronenergy/venus/wiki/dbus#grid-meter
	// also in system.py
	victronValues[0]["/Connected"] = dbus.MakeVariant(1)
	victronValues[1]["/Connected"] = dbus.MakeVariant("1")

	victronValues[0]["/CustomName"] = dbus.MakeVariant("Grid meter")
	victronValues[1]["/CustomName"] = dbus.MakeVariant("Grid meter")

	victronValues[0]["/DeviceInstance"] = dbus.MakeVariant(30)
	victronValues[1]["/DeviceInstance"] = dbus.MakeVariant("30")

	// also in system.py
	victronValues[0]["/DeviceType"] = dbus.MakeVariant(71)
	victronValues[1]["/DeviceType"] = dbus.MakeVariant("71")

	victronValues[0]["/ErrorCode"] = dbus.MakeVariantWithSignature(0, dbus.SignatureOf(123))
	victronValues[1]["/ErrorCode"] = dbus.MakeVariant("0")

	victronValues[0]["/FirmwareVersion"] = dbus.MakeVariant(2)
	victronValues[1]["/FirmwareVersion"] = dbus.MakeVariant("2")

	// also in system.py
	victronValues[0]["/Mgmt/Connection"] = dbus.MakeVariant("/dev/ttyUSB0")
	victronValues[1]["/Mgmt/Connection"] = dbus.MakeVariant("/dev/ttyUSB0")

	victronValues[0]["/Mgmt/ProcessName"] = dbus.MakeVariant("/opt/color-control/dbus-cgwacs/dbus-cgwacs")
	victronValues[1]["/Mgmt/ProcessName"] = dbus.MakeVariant("/opt/color-control/dbus-cgwacs/dbus-cgwacs")

	victronValues[0]["/Mgmt/ProcessVersion"] = dbus.MakeVariant("1.8.0")
	victronValues[1]["/Mgmt/ProcessVersion"] = dbus.MakeVariant("1.8.0")

	victronValues[0]["/Position"] = dbus.MakeVariantWithSignature(0, dbus.SignatureOf(123))
	victronValues[1]["/Position"] = dbus.MakeVariant("0")

	// also in system.py
	victronValues[0]["/ProductId"] = dbus.MakeVariant(45058)
	victronValues[1]["/ProductId"] = dbus.MakeVariant("45058")

	// also in system.py
	victronValues[0]["/ProductName"] = dbus.MakeVariant("Grid meter")
	victronValues[1]["/ProductName"] = dbus.MakeVariant("Grid meter")

	victronValues[0]["/Serial"] = dbus.MakeVariant("BP98305081235")
	victronValues[1]["/Serial"] = dbus.MakeVariant("BP98305081235")

	// Provide some initial values... note that the values must be a valid formt otherwise dbus_systemcalc.py exits like this:
	//@400000005ecc11bf3782b374   File "/opt/victronenergy/dbus-systemcalc-py/dbus_systemcalc.py", line 386, in _handletimertick
	//@400000005ecc11bf37aa251c     self._updatevalues()
	//@400000005ecc11bf380e74cc   File "/opt/victronenergy/dbus-systemcalc-py/dbus_systemcalc.py", line 678, in _updatevalues
	//@400000005ecc11bf383ab4ec     c = _safeadd(c, p, pvpower)
	//@400000005ecc11bf386c9674   File "/opt/victronenergy/dbus-systemcalc-py/sc_utils.py", line 13, in safeadd
	//@400000005ecc11bf387b28ec     return sum(values) if values else None
	//@400000005ecc11bf38b2bb7c TypeError: unsupported operand type(s) for +: 'int' and 'unicode'
	//

	for _, lookup := range actualLookup {
		val := 0.0
		victronValues[0][objectpath(lookup.Path)] = dbus.MakeVariant(val)
		victronValues[1][objectpath(lookup.Path)] = dbus.MakeVariant(fmt.Sprintf("%.1f %s", val, lookup.Unit))
	}
}

var basicPaths []dbus.ObjectPath = []dbus.ObjectPath{
	"/",
	"/Connected",
	"/CustomName",
	"/DeviceInstance",
	"/DeviceType",
	"/ErrorCode",
	"/FirmwareVersion",
	"/Mgmt/Connection",
	"/Mgmt/ProcessName",
	"/Mgmt/ProcessVersion",
	"/Position",
	"/ProductId",
	"/ProductName",
	"/Serial",
}

var receivedData bool = true

func connectionAliveCheck() {
	const timeout time.Duration = 30
	previousState := receivedData

	for {
		// check if we processed data the last 15s
		receivedData = false

		// wait some time
		time.Sleep(timeout * time.Second)

		if previousState != receivedData {
			if !receivedData {
				log.Errorf("offline. ooops, no incoming data for %ds... setting state to disconnected", timeout)
				publishDataString(0, "0", "/Connected")
			} else {
				log.Errorf("back online")
				publishDataString(1, "1", "/Connected")
			}
		}

		previousState = receivedData
	}
}

func multicastListener() {
	multicast.Listen(address, msgHandler)
	log.Panic("Error: We terminated.... how did we ever get here?")
}

func main() {

	// run connection check function
	go connectionAliveCheck()

	// run multicast listener
	go multicastListener()

	// init paths
	initVictronValues()

	// Some of the victron stuff requires it be called grid.cgwacs... using the only known valid value (from the simulator)
	// This can _probably_ be changed as long as it matches com.victronenergy.grid.cgwacs_*
	defer conn.Close()
	reply, err := conn.RequestName("com.victronenergy.grid.cgwacs_ttyUSB0_di30_mb1", dbus.NameFlagDoNotQueue)
	if err != nil {
		log.Error("Something went horribly wrong in the dbus connection. Will run in print only debug mode.")
	} else {
		if reply != dbus.RequestNameReplyPrimaryOwner {
			log.Panic("name cgwacs_ttyUSB0_di30_mb1 already taken on dbus.")
			os.Exit(1)
		}

		for i, s := range basicPaths {
			log.Debugf("Registering dbus basic path #%d: %s", i, s)
			conn.Export(objectpath(s), s, "com.victronenergy.BusItem")
			conn.Export(introspect.Introspectable(intro), s, "org.freedesktop.DBus.Introspectable")
		}

		for _, lookup := range actualLookup {
			s := dbus.ObjectPath(lookup.Path)
			log.Debugf("Registering dbus update path: %s", s)
			conn.Export(s, s, "com.victronenergy.BusItem")
			conn.Export(introspect.Introspectable(intro), s, "org.freedesktop.DBus.Introspectable")
		}

		log.Info("Successfully connected to dbus and registered as a meter... Commencing reading of the SMA meter")
	}

	dbusConnected = true

	select {}
}

const OBIS_ID_COUNTER = 8
const OBIS_ID_MEASURE = 4
const OBIS_ID_VERSION = 0

type LookupEntry struct {
	Path       string
	Unit       string
	Divider    float64
	Calculated bool
}

var actualLookup map[int]LookupEntry = map[int]LookupEntry{
	// real measurements
	(OBIS_ID_MEASURE | 1<<8): LookupEntry{"/Ac/Power", "W", 10.0, true},

	(OBIS_ID_MEASURE | 21<<8): LookupEntry{"/Ac/L1/Power", "W", 10.0, true},
	(OBIS_ID_MEASURE | 41<<8): LookupEntry{"/Ac/L2/Power", "W", 10.0, true},
	(OBIS_ID_MEASURE | 61<<8): LookupEntry{"/Ac/L3/Power", "W", 10.0, true},

	(OBIS_ID_MEASURE | 32<<8): LookupEntry{"/Ac/L1/Voltage", "V", 1000.0, false},
	(OBIS_ID_MEASURE | 52<<8): LookupEntry{"/Ac/L2/Voltage", "V", 1000.0, false},
	(OBIS_ID_MEASURE | 72<<8): LookupEntry{"/Ac/L3/Voltage", "V", 1000.0, false},

	(OBIS_ID_MEASURE | 31<<8): LookupEntry{"/Ac/L1/Current", "A", 1000.0, false},
	(OBIS_ID_MEASURE | 51<<8): LookupEntry{"/Ac/L2/Current", "A", 1000.0, false},
	(OBIS_ID_MEASURE | 71<<8): LookupEntry{"/Ac/L3/Current", "A", 1000.0, false},

	// counters
	(OBIS_ID_COUNTER | 21<<8): LookupEntry{"/Ac/L1/Energy/Forward", "kWh", 3600000.0, false},
	(OBIS_ID_COUNTER | 41<<8): LookupEntry{"/Ac/L2/Energy/Forward", "kWh", 3600000.0, false},
	(OBIS_ID_COUNTER | 61<<8): LookupEntry{"/Ac/L3/Energy/Forward", "kWh", 3600000.0, false},

	(OBIS_ID_COUNTER | 22<<8): LookupEntry{"/Ac/L1/Energy/Reverse", "kWh", 3600000.0, false},
	(OBIS_ID_COUNTER | 42<<8): LookupEntry{"/Ac/L2/Energy/Reverse", "kWh", 3600000.0, false},
	(OBIS_ID_COUNTER | 62<<8): LookupEntry{"/Ac/L3/Energy/Reverse", "kWh", 3600000.0, false},

	(OBIS_ID_COUNTER | 1<<8): LookupEntry{"/Ac/Energy/Forward", "kWh", 3600000.0, false},
	(OBIS_ID_COUNTER | 2<<8): LookupEntry{"/Ac/Energy/Reverse", "kWh", 3600000.0, false},
}

func msgHandler(src *net.UDPAddr, n int, b []byte) {
	// decode header identifier
	if bytes.Compare(b[0:4], []byte{'S', 'M', 'A', 0}) != 0 {
		log.Error("invalid header, expected SMA")
		return
	}

	// decode uid
	uid := binary.BigEndian.Uint32(b[4:8])
	log.Debugf("got uid 0x%08X\n", uid)

	// decode data length
	dlen := int(binary.BigEndian.Uint16(b[12:14]))
	log.Infof("processing incoming packet (len %d)\n", dlen)

	// verify correct tag
	tagExpected := 0x0010
	tagReceived := int(binary.BigEndian.Uint16(b[14:16]))
	if tagReceived != tagExpected {
		log.Fatalf("invalid tag, got 0x%04X, expected 0x%04\n", tagReceived, tagExpected)
	}

	// verify protocol id
	idExpected := 0x6069
	idReceived := int(binary.BigEndian.Uint16(b[16:18]))
	if idReceived != idExpected {
		log.Fatalf("invalid protocol id, got 0x%04X, expected 0x%04\n", idReceived, idExpected)
	}

	// lets fetch all data we can and store it in a temporary map
	var dataLookup map[int]int = make(map[int]int)
	for position := 20; position < dlen; {
		// new pointer to data
		datablob := b[position:]
		// fetch measurement id and type
		measurementType := int(datablob[2])
		measurementChannel := int(datablob[0])<<8 | int(datablob[1])
		id := measurementChannel<<8 | measurementType

		log.Tracef("decoded obis: type = %2d, channel = %2d -> id = 0x%06X\n", measurementType, measurementChannel, id)

		// store data
		if measurementType == OBIS_ID_COUNTER {
			// data uses 8 bytes
			dataLookup[id] = int(binary.BigEndian.Uint64(datablob[4 : 4+8]))
		} else {
			// data is using 4 bytes
			dataLookup[id] = int(binary.BigEndian.Uint32(datablob[4 : 4+4]))
		}

		// increment
		if measurementType == OBIS_ID_COUNTER {
			position += 4 + 8
		} else {
			position += 4 + 4
		}
	}

	// process incoming data, try to fetch all we need
	for id, lookup := range actualLookup {
		val, ok := dataLookup[id]
		if ok {
			// most of the data can be forwarded as it is, some needs to be calculated
			if lookup.Calculated == false {
				// this one is easy, forward data
				valFloat := float64(val) / lookup.Divider
				publishData(valFloat, lookup.Path, lookup.Unit)
			} else {
				// we need to calculate the data
				// e.g. for power we need to calc it based on forward and reverse values
				// check if we have the reverse value as well:
				val2, ok2 := dataLookup[id+(1<<8)]
				if !ok2 {
					log.Errorf("failed to fetch reverse value for %s\n", lookup.Path)
				} else {
					forward := float64(val) / lookup.Divider
					reverse := float64(val2) / lookup.Divider
					sum := forward - reverse
					publishData(sum, lookup.Path, lookup.Unit)
				}
			}
		}
	}

	// tell the alive check that we got data!
	receivedData = true
}

func publishDataString(value float64, text string, path string) {
	log.Debugf("publishing %-30s = %10.2f [%s]\n", path, value, text)
	if dbusConnected {
		emit := make(map[string]dbus.Variant)
		emit["Text"] = dbus.MakeVariant(text)
		emit["Value"] = dbus.MakeVariant(value)
		victronValues[0][objectpath(path)] = emit["Value"]
		victronValues[1][objectpath(path)] = emit["Text"]
		conn.Emit(dbus.ObjectPath(path), "com.victronenergy.BusItem.PropertiesChanged", emit)
	}
}

func publishData(value float64, path string, unit string) {
	publishDataString(value, fmt.Sprintf("%d%s", int(value), unit), path)
}
