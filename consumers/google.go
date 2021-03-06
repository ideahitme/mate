package consumers

import (
	"errors"
	"fmt"
	"strings"

	"github.bus.zalan.do/teapot/mate/pkg"

	log "github.com/Sirupsen/logrus"
	"golang.org/x/net/context"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/dns/v1"
	"gopkg.in/alecthomas/kingpin.v2"
)

const (
	heritageLabel = "heritage=mate"
	labelPrefix   = "mate/record-group-id="
)

type googleDNSConsumer struct {
	client *dns.Service
}

func init() {
	//	kingpin.Flag("gcloud-domain", "The DNS domain to create DNS entries under.").StringVar(params.domain)
	kingpin.Flag("google-project", "Project ID that manages the zone").StringVar(&params.project)
	kingpin.Flag("google-zone", "Name of the zone to manage.").StringVar(&params.zone)
	kingpin.Flag("google-record-group-id", "Name of the zone to manage.").StringVar(&params.recordGroupID)
}

func NewGoogleDNS() (Consumer, error) {
	if params.zone == "" {
		return nil, errors.New("Please provide --google-zone")
	}

	if params.project == "" {
		return nil, errors.New("Please provide --google-project")
	}

	if params.recordGroupID == "" {
		return nil, errors.New("Please provide --google-record-group-id")
	}

	// if params.zone == "" {
	// 	params.zone = strings.Replace(params.domain, ".", "-", -1)
	// 	log.Infof("No --zone provided, inferring from domain: '%s'.", params.zone)
	// }

	gcloud, err := google.DefaultClient(context.Background(), dns.NdevClouddnsReadwriteScope)
	if err != nil {
		return nil, fmt.Errorf("Error creating default client: %v", err)
	}

	client, err := dns.New(gcloud)
	if err != nil {
		return nil, fmt.Errorf("Error creating DNS service: %v", err)
	}

	return &googleDNSConsumer{
		client: client,
	}, nil
}

func (d *googleDNSConsumer) Sync(endpoints []*pkg.Endpoint) error {
	currentRecords, err := d.currentRecords()
	if err != nil {
		return err
	}

	log.Debugln("Current records:")
	d.printRecords(currentRecords)

	change := new(dns.Change)

	records := make([]*pkg.Endpoint, 0)

	for _, e := range endpoints {
		record, exists := currentRecords[e.DNSName]

		if !exists || exists && isResponsible(record) {
			records = append(records, e)
		}
	}

	for _, svc := range records {
		change.Additions = append(change.Additions,
			&dns.ResourceRecordSet{
				Name:    svc.DNSName,
				Rrdatas: []string{svc.IP},
				Ttl:     300,
				Type:    "A",
			},
			&dns.ResourceRecordSet{
				Name:    svc.DNSName,
				Rrdatas: []string{heritageLabel, labelPrefix + params.recordGroupID},
				Ttl:     300,
				Type:    "TXT",
			},
		)
	}

	for _, r := range currentRecords {
		if isResponsible(r) {
			change.Deletions = append(change.Deletions,
				&dns.ResourceRecordSet{
					Name:    r.Name,
					Rrdatas: r.Rrdatas,
					Ttl:     r.Ttl,
					Type:    r.Type,
				},
				&dns.ResourceRecordSet{
					Name:    r.Name,
					Rrdatas: []string{heritageLabel, labelPrefix + params.recordGroupID},
					Ttl:     r.Ttl,
					Type:    "TXT",
				},
			)
		}
	}

	err = d.applyChange(change)
	if err != nil {
		return fmt.Errorf("Error applying change for %s/%s: %v", params.project, params.zone, err)
	}

	return nil
}

func (d *googleDNSConsumer) Process(endpoint *pkg.Endpoint) error {
	change := new(dns.Change)

	change.Additions = []*dns.ResourceRecordSet{
		{
			Name:    endpoint.DNSName,
			Rrdatas: []string{endpoint.IP},
			Ttl:     300,
			Type:    "A",
		},
		&dns.ResourceRecordSet{
			Name:    endpoint.DNSName,
			Rrdatas: []string{heritageLabel, labelPrefix + params.recordGroupID},
			Ttl:     300,
			Type:    "TXT",
		},
	}

	err := d.applyChange(change)
	if err != nil {
		return fmt.Errorf("Error applying change for %s/%s: %v", params.project, params.zone, err)
	}

	return nil
}

func (d *googleDNSConsumer) applyChange(change *dns.Change) error {
	if len(change.Additions) == 0 && len(change.Deletions) == 0 {
		log.Infof("Didn't submit change (no changes)")
		return nil
	}

	_, err := d.client.Changes.Create(params.project, params.zone, change).Do()
	if err != nil {
		if strings.Contains(err.Error(), "alreadyExists") {
			log.Warnf("Cannot update some DNS records (already exist)")
			return nil
		}
		return fmt.Errorf("Unable to create change for %s/%s: %v", params.project, params.zone, err)
	}

	return nil
}

func (d *googleDNSConsumer) currentRecords() (map[string]*dns.ResourceRecordSet, error) {
	resp, err := d.client.ResourceRecordSets.List(params.project, params.zone).Do()
	if err != nil {
		return nil, fmt.Errorf("Error getting DNS records from %s/%s: %v", params.project, params.zone, err)
	}

	records := make(map[string]*dns.ResourceRecordSet)

	for _, r := range resp.Rrsets {
		if r.Type == "TXT" {
			records[r.Name] = r
		} else {
			if _, exists := records[r.Name]; !exists {
				records[r.Name] = nil
			}
		}
	}

	return records, nil
}

func (d *googleDNSConsumer) printRecords(records map[string]*dns.ResourceRecordSet) {
	for _, r := range records {
		if isResponsible(r) {
			log.Debugln(" ", r.Name, r.Type, r.Rrdatas)
		}
	}
}

func isResponsible(record *dns.ResourceRecordSet) bool {
	return record != nil && labelsMatch(record.Rrdatas)
}

func labelsMatch(labels []string) bool {
	return len(labels) == 2 &&
		strings.Trim(labels[0], `"`) == heritageLabel &&
		strings.Trim(labels[1], `"`) == labelPrefix+params.recordGroupID
}
