package driver

import (
	"fmt"
	"net"
	"sync"

	"github.com/docker/docker/pkg/stringid"
	networkapi "github.com/docker/go-plugins-helpers/network"
	"github.com/docker/libnetwork/datastore"
	"github.com/docker/libnetwork/netlabel"
	"github.com/docker/libnetwork/netutils"
	"github.com/docker/libnetwork/ns"
	"github.com/docker/libnetwork/options"
	"github.com/docker/libnetwork/osl"
	"github.com/docker/libnetwork/types"
	"github.com/sirupsen/logrus"
)

const networkType = "macvlan_noipam" //driverType

var driverModeOpt = "macvlan" + modeOpt // mode --option macvlan_mode

const (
	vethLen             = 7
	containerVethPrefix = "eth"
	vethPrefix          = "veth"
	macvlanType         = networkType // driver type name
	modePrivate         = "private"   // macvlan mode private
	modeVepa            = "vepa"      // macvlan mode vepa
	modeBridge          = "bridge"    // macvlan mode bridge
	modePassthru        = "passthru"  // macvlan mode passthrough
	parentOpt           = "parent"    // parent interface -o parent
	modeOpt             = "_mode"     // macvlan mode ux opt suffix
)

type driver struct {
	sync.Mutex
	networks networkTable
	store    datastore.DataStore
}

type endpointTable map[string]*endpoint

type networkTable map[string]*network

type endpoint struct {
	id       string
	nid      string
	mac      net.HardwareAddr
	srcName  string
	dbIndex  uint64
	dbExists bool
}

type network struct {
	id        string
	sbox      osl.Sandbox
	endpoints endpointTable
	driver    *driver
	config    *configuration
	sync.Mutex
}

func NewDriver() (*driver, error) {
	d := &driver{
		networks: make(networkTable),
	}
	err := d.initStore()
	logrus.Errorf("%s", err)

	if d.store == nil {
		logrus.Error("Store not initialized")
	} else {
		logrus.Info("Store is initialized")
	}

	return d, nil
}

func (d *driver) GetCapabilities() (*networkapi.CapabilitiesResponse, error) {
	logrus.Infof("Handling GetCapabilities")
	return &networkapi.CapabilitiesResponse{Scope: "local"}, nil
}

func (d *driver) AllocateNetwork(allocateNetworkRequest *networkapi.AllocateNetworkRequest) (*networkapi.AllocateNetworkResponse, error) {
	logrus.Infof("Handling AllocateNetwork")
	return nil, nil
}

func (d *driver) FreeNetwork(freeNetworkRequest *networkapi.FreeNetworkRequest) error {
	logrus.Infof("Handling FreeNetwork")
	return nil
}

func (d *driver) CreateNetwork(req *networkapi.CreateNetworkRequest) error {
	logrus.Infof("Handling CreateNetwork %s", req)
	defer osl.InitOSContext()()

	// reject a non null v4 network
	if len(req.IPv4Data) != 0 && req.IPv4Data[0].Pool != "0.0.0.0/0" {
		return fmt.Errorf("ipv4 pool is not empty")
	}
	// parse and validate the config and bind to networkConfiguration
	config, err := parseNetworkOptions(req.NetworkID, req.Options)
	if err != nil {
		return err
	}
	config.ID = req.NetworkID

	// verify the macvlan mode from -o macvlan_mode option
	switch config.MacvlanMode {
	case "", modeBridge:
		// default to macvlan bridge mode if -o macvlan_mode is empty
		config.MacvlanMode = modeBridge
	case modePrivate:
		config.MacvlanMode = modePrivate
	case modePassthru:
		config.MacvlanMode = modePassthru
	case modeVepa:
		config.MacvlanMode = modeVepa
	default:
		return fmt.Errorf("requested macvlan mode '%s' is not valid, 'bridge' mode is the macvlan driver default", config.MacvlanMode)
	}
	// loopback is not a valid parent link
	if config.Parent == "lo" {
		return fmt.Errorf("loopback interface is not a valid %s parent link", macvlanType)
	}
	// if parent interface not specified, create a dummy type link to use named dummy+net_id
	if config.Parent == "" {
		config.Parent = getDummyName(stringid.TruncateID(config.ID))
	}
	foundExisting, err := d.createNetwork(config)
	if err != nil {
		return err
	}

	if foundExisting {
		return types.InternalMaskableErrorf("restoring existing network %s", config.ID)
	}

	// update persistent db, rollback on fail
	err = d.storeUpdate(config)
	if err != nil {
		d.deleteNetwork(config.ID)
		logrus.Debugf("encountered an error rolling back a network create for %s : %v", config.ID, err)
		return err
	}

	return nil
}

func (d *driver) DeleteNetwork(req *networkapi.DeleteNetworkRequest) error {
	logrus.Infof("Handling DeleteNetwork %s", req)
	defer osl.InitOSContext()()
	n := d.network(req.NetworkID)
	if n == nil {
		return fmt.Errorf("network id %s not found", req.NetworkID)
	}
	// if the driver created the slave interface, delete it, otherwise leave it
	if ok := n.config.CreatedSlaveLink; ok {
		// if the interface exists, only delete if it matches iface.vlan or dummy.net_id naming
		if ok := parentExists(n.config.Parent); ok {
			// only delete the link if it is named the net_id
			if n.config.Parent == getDummyName(stringid.TruncateID(req.NetworkID)) {
				err := delDummyLink(n.config.Parent)
				if err != nil {
					logrus.Debugf("link %s was not deleted, continuing the delete network operation: %v",
						n.config.Parent, err)
				}
			} else {
				// only delete the link if it matches iface.vlan naming
				err := delVlanLink(n.config.Parent)
				if err != nil {
					logrus.Debugf("link %s was not deleted, continuing the delete network operation: %v",
						n.config.Parent, err)
				}
			}
		}
	}
	for _, ep := range n.endpoints {
		if link, err := ns.NlHandle().LinkByName(ep.srcName); err == nil {
			if err := ns.NlHandle().LinkDel(link); err != nil {
				logrus.WithError(err).Warnf("Failed to delete interface (%s)'s link on endpoint (%s) delete", ep.srcName, ep.id)
			}
		}

		if err := d.storeDelete(ep); err != nil {
			logrus.Warnf("Failed to remove macvlan endpoint %.7s from store: %v", ep.id, err)
		}
	}
	// delete the *network
	d.deleteNetwork(req.NetworkID)
	// delete the network record from persistent cache
	err := d.storeDelete(n.config)
	if err != nil {
		return fmt.Errorf("error deleting deleting id %s from datastore: %v", req.NetworkID, err)
	}
	return nil
}

func (d *driver) CreateEndpoint(req *networkapi.CreateEndpointRequest) (*networkapi.CreateEndpointResponse, error) {
	logrus.Infof("Handling CreateEndpoint")
	defer osl.InitOSContext()()

	if err := validateID(req.NetworkID, req.EndpointID); err != nil {
		return nil, err
	}
	n, err := d.getNetwork(req.NetworkID)
	if err != nil {
		return nil, fmt.Errorf("network id %q not found", req.NetworkID)
	}
	ep := &endpoint{
		id:  req.EndpointID,
		nid: req.NetworkID,
		mac: net.HardwareAddr(req.Interface.MacAddress),
	}

	if ep.mac == nil {
		ep.mac = netutils.GenerateMACFromIP(nil)
	}

	if err := d.storeUpdate(ep); err != nil {
		return nil, fmt.Errorf("failed to save macvlan endpoint %.7s to store: %v", ep.id, err)
	}

	n.addEndpoint(ep)

	return &networkapi.CreateEndpointResponse{
		Interface: &networkapi.EndpointInterface{
			MacAddress: ep.mac.String(),
		},
	}, nil
}

func (d *driver) DeleteEndpoint(req *networkapi.DeleteEndpointRequest) error {
	logrus.Infof("Handling DeleteEndpoint")

	defer osl.InitOSContext()()
	if err := validateID(req.NetworkID, req.EndpointID); err != nil {
		return err
	}
	n := d.network(req.NetworkID)
	if n == nil {
		return fmt.Errorf("network id %q not found", req.NetworkID)
	}
	ep := n.endpoint(req.EndpointID)
	if ep == nil {
		return fmt.Errorf("endpoint id %q not found", req.EndpointID)
	}
	if link, err := ns.NlHandle().LinkByName(ep.srcName); err == nil {
		if err := ns.NlHandle().LinkDel(link); err != nil {
			logrus.WithError(err).Warnf("Failed to delete interface (%s)'s link on endpoint (%s) delete", ep.srcName, ep.id)
		}
	}

	if err := d.storeDelete(ep); err != nil {
		logrus.Warnf("Failed to remove macvlan endpoint %.7s from store: %v", ep.id, err)
	}

	n.deleteEndpoint(ep.id)

	return nil
}

func (d *driver) EndpointInfo(infoRequest *networkapi.InfoRequest) (*networkapi.InfoResponse, error) {
	logrus.Infof("Handling EndpointInfo")
	return nil, nil
}

func (d *driver) Join(req *networkapi.JoinRequest) (*networkapi.JoinResponse, error) {
	logrus.Infof("Handling Join %s", req)

	defer osl.InitOSContext()()
	n, err := d.getNetwork(req.NetworkID)
	if err != nil {
		return nil, err
	}
	endpoint := n.endpoint(req.EndpointID)
	if endpoint == nil {
		return nil, fmt.Errorf("could not find endpoint with id %s", req.EndpointID)
	}
	// generate a name for the iface that will be renamed to eth0 in the sbox
	containerIfName, err := netutils.GenerateIfaceName(ns.NlHandle(), vethPrefix, vethLen)
	if err != nil {
		return nil, fmt.Errorf("error generating an interface name: %s", err)
	}
	// create the netlink macvlan interface
	vethName, err := createMacVlan(containerIfName, n.config.Parent, n.config.MacvlanMode)
	if err != nil {
		return nil, err
	}
	// bind the generated iface name to the endpoint
	endpoint.srcName = vethName
	ep := n.endpoint(req.EndpointID)
	if ep == nil {
		return nil, fmt.Errorf("could not find endpoint with id %s", req.EndpointID)
	}

	/*iNames := jinfo.InterfaceName()
	err = iNames.SetNames(vethName, containerVethPrefix)
	if err != nil {
		return err
	}*/
	if err := d.storeUpdate(ep); err != nil {
		return nil, fmt.Errorf("failed to save macvlan endpoint %.7s to store: %v", ep.id, err)
	}

	return &networkapi.JoinResponse{
		InterfaceName: networkapi.InterfaceName{
			SrcName:   vethName,
			DstPrefix: containerVethPrefix,
		},
		DisableGatewayService: true,
	}, nil
}

func (d *driver) Leave(req *networkapi.LeaveRequest) error {
	logrus.Infof("Handling Leave")

	defer osl.InitOSContext()()
	network, err := d.getNetwork(req.NetworkID)
	if err != nil {
		return err
	}
	endpoint, err := network.getEndpoint(req.EndpointID)
	if err != nil {
		return err
	}
	if endpoint == nil {
		return fmt.Errorf("could not find endpoint with id %s", req.EndpointID)
	}

	return nil
}

func (d *driver) DiscoverNew(discoveryNotification *networkapi.DiscoveryNotification) error {
	logrus.Infof("Handling DiscoverNew")
	return nil
}

func (d *driver) DiscoverDelete(discoveryNotification *networkapi.DiscoveryNotification) error {
	logrus.Infof("Handling DiscoverDelete")
	return nil
}

func (d *driver) ProgramExternalConnectivity(programExternalConnectivityRequest *networkapi.ProgramExternalConnectivityRequest) error {
	logrus.Infof("Handling ProgramExternalConnectivity")
	return nil
}

func (d *driver) RevokeExternalConnectivity(revokeExternalConnectivityRequest *networkapi.RevokeExternalConnectivityRequest) error {
	logrus.Infof("Handling RevokeExternalConnectivity")
	return nil
}

func (d *driver) Type() string {
	return networkType
}

func (d *driver) IsBuiltIn() bool {
	return false
}

// createNetwork is used by new network callbacks and persistent network cache
func (d *driver) createNetwork(config *configuration) (bool, error) {
	foundExisting := false
	networkList := d.getNetworks()
	for _, nw := range networkList {
		if config.Parent == nw.config.Parent {
			if config.ID != nw.config.ID {
				return false, fmt.Errorf("network %s is already using parent interface %s",
					getDummyName(stringid.TruncateID(nw.config.ID)), config.Parent)
			}
			logrus.Debugf("Create Network for the same ID %s\n", config.ID)
			foundExisting = true
			break
		}
	}
	if !parentExists(config.Parent) {
		// Create a dummy link if a dummy name is set for parent
		if dummyName := getDummyName(stringid.TruncateID(config.ID)); dummyName == config.Parent {
			err := createDummyLink(config.Parent, dummyName)
			if err != nil {
				return false, err
			}
			config.CreatedSlaveLink = true
			// notify the user in logs that they have limited communications
			logrus.Debugf("Empty -o parent= limit communications to other containers inside of network: %s",
				config.Parent)
		} else {
			// if the subinterface parent_iface.vlan_id checks do not pass, return err.
			//  a valid example is 'eth0.10' for a parent iface 'eth0' with a vlan id '10'
			err := createVlanLink(config.Parent)
			if err != nil {
				return false, err
			}
			// if driver created the networks slave link, record it for future deletion
			config.CreatedSlaveLink = true
		}
	}
	if !foundExisting {
		n := &network{
			id:        config.ID,
			driver:    d,
			endpoints: endpointTable{},
			config:    config,
		}
		// add the network
		d.addNetwork(n)
	}

	return foundExisting, nil
}

// parseNetworkOptions parses docker network options
func parseNetworkOptions(id string, option options.Generic) (*configuration, error) {
	var (
		err    error
		config = &configuration{}
	)
	// parse generic labels first
	if genData, ok := option[netlabel.GenericData]; ok && genData != nil {
		if config, err = parseNetworkGenericOptions(genData); err != nil {
			return nil, err
		}
	}
	if val, ok := option[netlabel.Internal]; ok {
		if internal, ok := val.(bool); ok && internal {
			config.Internal = true
		}
	}

	return config, nil
}

// parseNetworkGenericOptions parses generic driver docker network options
func parseNetworkGenericOptions(data interface{}) (*configuration, error) {
	var (
		err    error
		config *configuration
	)
	switch opt := data.(type) {
	case *configuration:
		config = opt
	case map[string]string:
		config = &configuration{}
		err = config.fromOptions(opt)
	case options.Generic:
		var opaqueConfig interface{}
		if opaqueConfig, err = options.GenerateFromModel(opt, config); err == nil {
			config = opaqueConfig.(*configuration)
		}
	case map[string]interface{}:
		logrus.Infof("It is map string interface %v", opt)
		config = &configuration{}
		for label, value := range opt {
			switch label {
			case parentOpt: // parse driver option '-o parent'
				config.Parent = value.(string)
				/*if stringvalue, ok := value.(string); !ok {
					logrus.Errorf("Value is not string for parent: %T", value)
				} else {
					config.Parent = stringvalue
					logrus.Infof("Parent is %s", stringvalue)
				}*/
			case driverModeOpt:
				// parse driver option '-o macvlan_mode'
				config.MacvlanMode = value.(string)
			default:
				logrus.Errorf("Unmacthed option key %s", label)
			}
		}
	default:
		err = types.BadRequestErrorf("unrecognized network configuration format %T: %v", opt, opt)
	}

	return config, err
}

// fromOptions binds the generic options to networkConfiguration to cache
func (config *configuration) fromOptions(labels map[string]string) error {
	for label, value := range labels {
		switch label {
		case parentOpt:
			// parse driver option '-o parent'
			config.Parent = value
		case driverModeOpt:
			// parse driver option '-o macvlan_mode'
			config.MacvlanMode = value
		}
	}

	return nil
}
