// Package deviceconfig models the WIRE FORMAT of one device's
// device-specific config file — the exact JSON shape found on disk at
// "<basePath>/devices/<usecase_name>/<device_id>.json" — and NOTHING
// else. See the sibling models/iotedge/usecase package for everything
// BUILT on top of this type: the templated file port (NewDeviceFile),
// the directory-listing port (NewDeviceDir/ListDeviceIDs), and the
// domain composition (DeviceConfig, ReadDeviceConfig/WriteDeviceConfig)
// that pairs a Manifest with its device ID.
package deviceconfig
