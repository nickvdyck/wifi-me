// +build darwin,!ios

package main

/*
#cgo LDFLAGS: -framework CoreFoundation -framework Security

#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
*/
import "C"
import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"unicode/utf8"
	"unsafe"
)

// Error int representing an error returned from the mac keyring api
type Error int

var (
	ErrorUnimplemented         = Error(C.errSecUnimplemented)
	ErrorParam                 = Error(C.errSecParam)
	ErrorAllocate              = Error(C.errSecAllocate)
	ErrorNotAvailable          = Error(C.errSecNotAvailable)
	ErrorAuthFailed            = Error(C.errSecAuthFailed)
	ErrorDuplicateItem         = Error(C.errSecDuplicateItem)
	ErrorItemNotFound          = Error(C.errSecItemNotFound)
	ErrorInteractionNotAllowed = Error(C.errSecInteractionNotAllowed)
	ErrorDecode                = Error(C.errSecDecode)
)

func checkError(errCode C.OSStatus) error {
	if errCode == C.errSecSuccess {
		return nil
	}
	return Error(errCode)
}

func (k Error) Error() string {
	var msg string
	// SecCopyErrorMessageString is only available on OSX, so derive manually.
	switch k {
	case ErrorItemNotFound:
		msg = fmt.Sprintf("Item not found (%d)", k)
	case ErrorDuplicateItem:
		msg = fmt.Sprintf("Duplicate item (%d)", k)
	case ErrorParam:
		msg = fmt.Sprintf("One or more parameters passed to the function were not valid (%d)", k)
	case -25243:
		msg = fmt.Sprintf("No access for item (%d)", k)
	default:
		msg = fmt.Sprintf("Keychain Error (%d)", k)
	}
	return msg
}

var (
	SecClassKey         = attrKey(C.CFTypeRef(C.kSecClass))
	ServiceKey          = attrKey(C.CFTypeRef(C.kSecAttrService))
	AccountKey          = attrKey(C.CFTypeRef(C.kSecAttrAccount))
	MatchLimitKey       = attrKey(C.CFTypeRef(C.kSecMatchLimit))
	ReturnDataKey       = attrKey(C.CFTypeRef(C.kSecReturnData))
	ReturnAttributesKey = attrKey(C.CFTypeRef(C.kSecReturnAttributes))
	AccessGroupKey      = attrKey(C.CFTypeRef(C.kSecAttrAccessGroup))
	LabelKey            = attrKey(C.CFTypeRef(C.kSecAttrLabel))
	DataKey             = attrKey(C.CFTypeRef(C.kSecValueData))
)

// QueryResult stores all possible results from queries.
// Not all fields are applicable all the time. Results depend on query.
type QueryResult struct {
	Service     string
	Account     string
	AccessGroup string
	Label       string
	Data        []byte
}

type Query map[string]interface{}

// Keyring represents a keyring object
type Keyring struct{}

func GetMacKeyringPassword(service string, account string) ([]byte, error) {
	query := make(Query)

	query[SecClassKey] = C.CFTypeRef(C.kSecClassGenericPassword)
	query[ServiceKey] = service
	query[AccountKey] = account
	query[MatchLimitKey] = C.CFTypeRef(C.kSecMatchLimitOne)
	query[ReturnDataKey] = true

	results, err := QueryItem(query)
	if err != nil {
		return nil, err
	}
	if len(results) > 1 {
		return nil, fmt.Errorf("Too many results")
	}
	if len(results) == 1 {
		return results[0].Data, nil
	}
	return nil, nil
}

func GetGenericPasswordAccounts(service string) ([]string, error) {
	query := make(Query)
	query[SecClassKey] = C.CFTypeRef(C.kSecClassGenericPassword)
	query[ServiceKey] = service
	query[MatchLimitKey] = C.CFTypeRef(C.kSecMatchLimitAll)
	query[ReturnAttributesKey] = true

	results, err := QueryItem(query)

	if err != nil {
		return nil, err
	}

	accounts := make([]string, 0, len(results))
	for _, r := range results {
		accounts = append(accounts, r.Account)
	}

	return accounts, nil
}

// QueryItem returns a list of query results.
func QueryItem(item Query) ([]QueryResult, error) {
	cfDict, err := ConvertMapToCFDictionary(item)
	if err != nil {
		return nil, err
	}
	defer Release(C.CFTypeRef(cfDict))

	var resultsRef C.CFTypeRef
	errCode := C.SecItemCopyMatching(cfDict, &resultsRef)
	if Error(errCode) == ErrorItemNotFound {
		return nil, nil
	}
	err = checkError(errCode)
	if err != nil {
		return nil, err
	}

	if err != nil {
		return nil, err
	}

	if resultsRef == nil {
		return nil, nil
	}
	defer Release(resultsRef)

	results := make([]QueryResult, 0, 1)

	typeID := C.CFGetTypeID(resultsRef)
	if typeID == C.CFArrayGetTypeID() {
		arr := CFArrayToArray(C.CFArrayRef(resultsRef))
		for _, ref := range arr {
			typeID := C.CFGetTypeID(ref)
			if typeID == C.CFDictionaryGetTypeID() {
				item, err := convertResult(C.CFDictionaryRef(ref))
				if err != nil {
					return nil, err
				}
				results = append(results, *item)
			} else {
				return nil, fmt.Errorf("Invalid result type (If you SetReturnRef(true) you should use QueryItemRef directly).")
			}
		}
	} else if typeID == C.CFDictionaryGetTypeID() {
		item, err := convertResult(C.CFDictionaryRef(resultsRef))
		if err != nil {
			return nil, err
		}
		results = append(results, *item)
	} else if typeID == C.CFDataGetTypeID() {
		b, err := CFDataToBytes(C.CFDataRef(resultsRef))
		if err != nil {
			return nil, err
		}
		item := QueryResult{Data: b}
		results = append(results, item)
	} else {
		return nil, fmt.Errorf("Invalid result type: %s", CFTypeDescription(resultsRef))
	}

	return results, nil
}

func convertResult(d C.CFDictionaryRef) (*QueryResult, error) {
	m := CFDictionaryToMap(C.CFDictionaryRef(d))
	result := QueryResult{}
	for k, v := range m {
		switch attrKey(k) {
		case ServiceKey:
			result.Service = CFStringToString(C.CFStringRef(v))
		case AccountKey:
			result.Account = CFStringToString(C.CFStringRef(v))
		case AccessGroupKey:
			result.AccessGroup = CFStringToString(C.CFStringRef(v))
		case LabelKey:
			result.Label = CFStringToString(C.CFStringRef(v))
		case DataKey:
			b, err := CFDataToBytes(C.CFDataRef(v))
			if err != nil {
				return nil, err
			}
			result.Data = b
			// default:
			// fmt.Printf("Unhandled key in conversion: %v = %v\n", cfTypeValue(k), cfTypeValue(v))
		}
	}
	return &result, nil
}

// Core functions

// GetVersion get keyring version
func GetVersion() uint32 {
	var versionRef C.UInt32
	C.SecKeychainGetVersion(&versionRef)
	return uint32(versionRef)
}

// Get an attribute key
func attrKey(ref C.CFTypeRef) string {
	return CFStringToString(C.CFStringRef(ref))
}

// Release release resources
func Release(ref C.CFTypeRef) {
	C.CFRelease(ref)
}

// BytesToCFData will return a CFDataRef and if non-nil, must be released with
// Release(ref).
func BytesToCFData(b []byte) (C.CFDataRef, error) {
	if uint64(len(b)) > math.MaxUint32 {
		return nil, errors.New("Data is too large")
	}
	var p *C.UInt8
	if len(b) > 0 {
		p = (*C.UInt8)(&b[0])
	}
	cfData := C.CFDataCreate(nil, p, C.CFIndex(len(b)))
	if cfData == nil {
		return nil, fmt.Errorf("CFDataCreate failed")
	}
	return cfData, nil
}

// CFDataToBytes converts CFData to bytes.
func CFDataToBytes(cfData C.CFDataRef) ([]byte, error) {
	return C.GoBytes(unsafe.Pointer(C.CFDataGetBytePtr(cfData)), C.int(C.CFDataGetLength(cfData))), nil
}

// MapToCFDictionary will return a CFDictionaryRef and if non-nil, must be
// released with Release(ref).
func MapToCFDictionary(m map[C.CFTypeRef]C.CFTypeRef) (C.CFDictionaryRef, error) {
	var keys, values []unsafe.Pointer
	for key, value := range m {
		keys = append(keys, unsafe.Pointer(key))
		values = append(values, unsafe.Pointer(value))
	}
	numValues := len(values)
	var keysPointer, valuesPointer *unsafe.Pointer
	if numValues > 0 {
		keysPointer = &keys[0]
		valuesPointer = &values[0]
	}
	cfDict := C.CFDictionaryCreate(nil, keysPointer, valuesPointer, C.CFIndex(numValues), &C.kCFTypeDictionaryKeyCallBacks, &C.kCFTypeDictionaryValueCallBacks)
	if cfDict == nil {
		return nil, fmt.Errorf("CFDictionaryCreate failed")
	}
	return cfDict, nil
}

// CFDictionaryToMap converts CFDictionaryRef to a map.
func CFDictionaryToMap(cfDict C.CFDictionaryRef) (m map[C.CFTypeRef]C.CFTypeRef) {
	count := C.CFDictionaryGetCount(cfDict)
	if count > 0 {
		keys := make([]C.CFTypeRef, count)
		values := make([]C.CFTypeRef, count)
		C.CFDictionaryGetKeysAndValues(cfDict, (*unsafe.Pointer)(&keys[0]), (*unsafe.Pointer)(&values[0]))
		m = make(map[C.CFTypeRef]C.CFTypeRef, count)
		for i := C.CFIndex(0); i < count; i++ {
			m[keys[i]] = values[i]
		}
	}
	return
}

// StringToCFString will return a CFStringRef and if non-nil, must be released with
// Release(ref).
func StringToCFString(s string) (C.CFStringRef, error) {
	if !utf8.ValidString(s) {
		return nil, errors.New("Invalid UTF-8 string")
	}
	if uint64(len(s)) > math.MaxUint32 {
		return nil, errors.New("String is too large")
	}

	bytes := []byte(s)
	var p *C.UInt8
	if len(bytes) > 0 {
		p = (*C.UInt8)(&bytes[0])
	}
	return C.CFStringCreateWithBytes(nil, p, C.CFIndex(len(s)), C.kCFStringEncodingUTF8, C.false), nil
}

// CFStringToString converts a CFStringRef to a string.
func CFStringToString(s C.CFStringRef) string {
	p := C.CFStringGetCStringPtr(s, C.kCFStringEncodingUTF8)
	if p != nil {
		return C.GoString(p)
	}
	length := C.CFStringGetLength(s)
	if length == 0 {
		return ""
	}
	maxBufLen := C.CFStringGetMaximumSizeForEncoding(length, C.kCFStringEncodingUTF8)
	if maxBufLen == 0 {
		return ""
	}
	buf := make([]byte, maxBufLen)
	var usedBufLen C.CFIndex
	_ = C.CFStringGetBytes(s, C.CFRange{0, length}, C.kCFStringEncodingUTF8, C.UInt8(0), C.false, (*C.UInt8)(&buf[0]), maxBufLen, &usedBufLen)
	return string(buf[:usedBufLen])
}

// ArrayToCFArray will return a CFArrayRef and if non-nil, must be released with
// Release(ref).
func ArrayToCFArray(a []C.CFTypeRef) C.CFArrayRef {
	var values []unsafe.Pointer
	for _, value := range a {
		values = append(values, unsafe.Pointer(value))
	}
	numValues := len(values)
	var valuesPointer *unsafe.Pointer
	if numValues > 0 {
		valuesPointer = &values[0]
	}
	return C.CFArrayCreate(nil, valuesPointer, C.CFIndex(numValues), &C.kCFTypeArrayCallBacks)
}

// CFArrayToArray converts a CFArrayRef to an array of CFTypes.
func CFArrayToArray(cfArray C.CFArrayRef) (a []C.CFTypeRef) {
	count := C.CFArrayGetCount(cfArray)
	if count > 0 {
		a = make([]C.CFTypeRef, count)
		C.CFArrayGetValues(cfArray, C.CFRange{0, count}, (*unsafe.Pointer)(&a[0]))
	}
	return
}

// Convertable knows how to convert an instance to a CFTypeRef.
type Convertable interface {
	Convert() (C.CFTypeRef, error)
}

// ConvertMapToCFDictionary converts a map to a CFDictionary and if non-nil,
// must be released with Release(ref).
func ConvertMapToCFDictionary(attr map[string]interface{}) (C.CFDictionaryRef, error) {
	m := make(map[C.CFTypeRef]C.CFTypeRef)
	for key, i := range attr {
		var valueRef C.CFTypeRef
		switch i.(type) {
		default:
			return nil, fmt.Errorf("Unsupported value type: %v", reflect.TypeOf(i))
		case C.CFTypeRef:
			valueRef = i.(C.CFTypeRef)
		case bool:
			if i == true {
				valueRef = C.CFTypeRef(C.kCFBooleanTrue)
			} else {
				valueRef = C.CFTypeRef(C.kCFBooleanFalse)
			}
		case []byte:
			bytesRef, err := BytesToCFData(i.([]byte))
			if err != nil {
				return nil, err
			}
			valueRef = C.CFTypeRef(bytesRef)
			defer Release(valueRef)
		case string:
			stringRef, err := StringToCFString(i.(string))
			if err != nil {
				return nil, err
			}
			valueRef = C.CFTypeRef(stringRef)
			defer Release(valueRef)
		case Convertable:
			convertedRef, err := (i.(Convertable)).Convert()
			if err != nil {
				return nil, err
			}
			valueRef = C.CFTypeRef(convertedRef)
			defer Release(valueRef)
		}
		keyRef, err := StringToCFString(key)
		if err != nil {
			return nil, err
		}
		m[C.CFTypeRef(keyRef)] = valueRef
	}

	cfDict, err := MapToCFDictionary(m)
	if err != nil {
		return nil, err
	}
	return cfDict, nil
}

// CFTypeDescription returns type string for CFTypeRef.
func CFTypeDescription(ref C.CFTypeRef) string {
	typeID := C.CFGetTypeID(ref)
	typeDesc := C.CFCopyTypeIDDescription(typeID)
	defer Release(C.CFTypeRef(typeDesc))
	return CFStringToString(typeDesc)
}

// Convert converts a CFTypeRef to a go instance.
func Convert(ref C.CFTypeRef) (interface{}, error) {
	typeID := C.CFGetTypeID(ref)
	if typeID == C.CFStringGetTypeID() {
		return CFStringToString(C.CFStringRef(ref)), nil
	} else if typeID == C.CFDictionaryGetTypeID() {
		return ConvertCFDictionary(C.CFDictionaryRef(ref))
	} else if typeID == C.CFArrayGetTypeID() {
		arr := CFArrayToArray(C.CFArrayRef(ref))
		results := make([]interface{}, 0, len(arr))
		for _, ref := range arr {
			v, err := Convert(ref)
			if err != nil {
				return nil, err
			}
			results = append(results, v)
			return results, nil
		}
	} else if typeID == C.CFDataGetTypeID() {
		b, err := CFDataToBytes(C.CFDataRef(ref))
		if err != nil {
			return nil, err
		}
		return b, nil
	} else if typeID == C.CFNumberGetTypeID() {
		return CFNumberToInterface(C.CFNumberRef(ref)), nil
	} else if typeID == C.CFBooleanGetTypeID() {
		if C.CFBooleanGetValue(C.CFBooleanRef(ref)) != 0 {
			return true, nil
		}
		return false, nil
	}

	return nil, fmt.Errorf("Invalid type: %s", CFTypeDescription(ref))
}

// ConvertCFDictionary converts a CFDictionary to map (deep).
func ConvertCFDictionary(d C.CFDictionaryRef) (map[interface{}]interface{}, error) {
	m := CFDictionaryToMap(C.CFDictionaryRef(d))
	result := make(map[interface{}]interface{})

	for k, v := range m {
		gk, err := Convert(k)
		if err != nil {
			return nil, err
		}
		gv, err := Convert(v)
		if err != nil {
			return nil, err
		}
		result[gk] = gv
	}
	return result, nil
}

// CFNumberToInterface converts the CFNumberRef to the most appropriate numeric
// type.
// This code is from github.com/kballard/go-osx-plist.
func CFNumberToInterface(cfNumber C.CFNumberRef) interface{} {
	typ := C.CFNumberGetType(cfNumber)
	switch typ {
	case C.kCFNumberSInt8Type:
		var sint C.SInt8
		C.CFNumberGetValue(cfNumber, typ, unsafe.Pointer(&sint))
		return int8(sint)
	case C.kCFNumberSInt16Type:
		var sint C.SInt16
		C.CFNumberGetValue(cfNumber, typ, unsafe.Pointer(&sint))
		return int16(sint)
	case C.kCFNumberSInt32Type:
		var sint C.SInt32
		C.CFNumberGetValue(cfNumber, typ, unsafe.Pointer(&sint))
		return int32(sint)
	case C.kCFNumberSInt64Type:
		var sint C.SInt64
		C.CFNumberGetValue(cfNumber, typ, unsafe.Pointer(&sint))
		return int64(sint)
	case C.kCFNumberFloat32Type:
		var float C.Float32
		C.CFNumberGetValue(cfNumber, typ, unsafe.Pointer(&float))
		return float32(float)
	case C.kCFNumberFloat64Type:
		var float C.Float64
		C.CFNumberGetValue(cfNumber, typ, unsafe.Pointer(&float))
		return float64(float)
	case C.kCFNumberCharType:
		var char C.char
		C.CFNumberGetValue(cfNumber, typ, unsafe.Pointer(&char))
		return byte(char)
	case C.kCFNumberShortType:
		var short C.short
		C.CFNumberGetValue(cfNumber, typ, unsafe.Pointer(&short))
		return int16(short)
	case C.kCFNumberIntType:
		var i C.int
		C.CFNumberGetValue(cfNumber, typ, unsafe.Pointer(&i))
		return int32(i)
	case C.kCFNumberLongType:
		var long C.long
		C.CFNumberGetValue(cfNumber, typ, unsafe.Pointer(&long))
		return int(long)
	case C.kCFNumberLongLongType:
		// This is the only type that may actually overflow us
		var longlong C.longlong
		C.CFNumberGetValue(cfNumber, typ, unsafe.Pointer(&longlong))
		return int64(longlong)
	case C.kCFNumberFloatType:
		var float C.float
		C.CFNumberGetValue(cfNumber, typ, unsafe.Pointer(&float))
		return float32(float)
	case C.kCFNumberDoubleType:
		var double C.double
		C.CFNumberGetValue(cfNumber, typ, unsafe.Pointer(&double))
		return float64(double)
	case C.kCFNumberCFIndexType:
		// CFIndex is a long
		var index C.CFIndex
		C.CFNumberGetValue(cfNumber, typ, unsafe.Pointer(&index))
		return int(index)
	case C.kCFNumberNSIntegerType:
		// We don't have a definition of NSInteger, but we know it's either an int or a long
		var nsInt C.long
		C.CFNumberGetValue(cfNumber, typ, unsafe.Pointer(&nsInt))
		return int(nsInt)
	}
	panic("Unknown CFNumber type")
}
