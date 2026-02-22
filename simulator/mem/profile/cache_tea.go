package profile

type CachePSVStatus int

const (
	IDLE CachePSVStatus = iota
	BASE
	TRANSLATION
	MISS
	LENS
)

var CachePSVStatusNames = []string{
	"idle",
	"base",
	"translation",
	"miss",
	"lens",
}

type CachePSV struct {
	Status CachePSVStatus
}

func NewCachePSV() *CachePSV {
	return &CachePSV{
		Status: IDLE,
	}
}

func (c *CachePSV) Update(status CachePSVStatus) {
	c.Status = status
}

func (c *CachePSV) GetStatus() CachePSVStatus {
	return c.Status
}

type CachePSVComponent interface {
	Attribute() CachePSVStatus
	SetProvider(provider CachePSVComponent)
}
