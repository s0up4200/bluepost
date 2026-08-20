package protocol

const (
	BusName       = "io.github.s0up4200.Bluepost"
	ObjectPath    = "/io/github/s0up4200/Bluepost"
	MessagesIface = BusName + ".Messages1"
	EventsIface   = BusName + ".Events1"
	ErrorPrefix   = BusName + ".Error"
)

const (
	MaxBMessageBytes       = 1 << 20
	MaxPhonebookBytes      = 64 << 20
	MaxVCardBytes          = 1 << 20
	MaxContactNameChars    = 512
	MaxContactAddressChars = 320
	MaxAddressesPerCard    = 64
	MaxPhonebookContacts   = 65_535
	MaxHistoryRecords      = 2_000
	MaxHistoryBytes        = 64 << 20
	MaxPublicBodyChars     = 32 << 10
	MaxDBusJSONBytes       = 8 << 20
	MaxRecentRecords       = 200
	MaxContactPage         = 150
	MaxOBEXOperations      = 256
)
