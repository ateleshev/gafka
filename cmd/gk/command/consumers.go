package command

import (
	"flag"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Shopify/sarama"
	"github.com/funkygao/columnize"
	"github.com/funkygao/gafka/ctx"
	"github.com/funkygao/gafka/zk"
	"github.com/funkygao/gocli"
	"github.com/funkygao/golib/color"
	"github.com/funkygao/golib/gofmt"
	gozk "github.com/samuel/go-zookeeper/zk"
)

type consumerGroupOffset struct {
	topic, partitionId string
	offset             string // comma fmt
	lag                string // comma fmt
}

type Consumers struct {
	Ui  cli.Ui
	Cmd string

	onlineOnly   bool
	ownerOnly    bool
	groupPattern string
	warnOnly     bool
	byHost       bool
	cleanup      bool
	confirmYes   bool
	zombie       bool
	topicPattern string
}

func (this *Consumers) Run(args []string) (exitCode int) {
	var (
		cluster string
		zone    string
	)
	cmdFlags := flag.NewFlagSet("consumers", flag.ContinueOnError)
	cmdFlags.Usage = func() { this.Ui.Output(this.Help()) }
	cmdFlags.StringVar(&zone, "z", ctx.ZkDefaultZone(), "")
	cmdFlags.StringVar(&cluster, "c", "", "")
	cmdFlags.StringVar(&this.groupPattern, "g", "", "")
	cmdFlags.BoolVar(&this.onlineOnly, "online", false, "")
	cmdFlags.BoolVar(&this.byHost, "byhost", false, "")
	cmdFlags.StringVar(&this.topicPattern, "t", "", "")
	cmdFlags.BoolVar(&this.zombie, "zb", false, "")
	cmdFlags.BoolVar(&this.warnOnly, "warn", false, "")
	cmdFlags.BoolVar(&this.ownerOnly, "own", false, "")
	cmdFlags.BoolVar(&this.cleanup, "cleanup", false, "")
	cmdFlags.BoolVar(&this.confirmYes, "yes", false, "")
	if err := cmdFlags.Parse(args); err != nil {
		return 1
	}

	if validateArgs(this, this.Ui).
		requireAdminRights("-cleanup").
		invalid(args) {
		return 2
	}

	if this.ownerOnly {
		this.onlineOnly = true
	}

	if zone == "" {
		forSortedZones(func(zkzone *zk.ZkZone) {
			switch {
			case this.cleanup:
				this.cleanupStaleConsumerGroups(zkzone, cluster)
			case this.byHost:
				this.printConsumersByHost(zkzone, cluster)
			default:
				this.printConsumersByGroupTable(zkzone, cluster)
			}
		})

		return
	}

	zkzone := zk.NewZkZone(zk.DefaultConfig(zone, ctx.ZoneZkAddrs(zone)))
	switch {
	case this.zombie:
		this.printZombies(zkzone, cluster)
	case this.cleanup:
		this.cleanupStaleConsumerGroups(zkzone, cluster)
	case this.byHost:
		this.printConsumersByHost(zkzone, cluster)
	default:
		this.printConsumersByGroupTable(zkzone, cluster)
	}

	return
}

func (this *Consumers) printZombies(zkzone *zk.ZkZone, clusterPattern string) {
	lines := []string{"Cluster|ZombieGroup"}
	zkzone.ForSortedClusters(func(zkcluster *zk.ZkCluster) {
		if !patternMatched(zkcluster.Name(), clusterPattern) {
			return
		}

		for _, zombie := range zkcluster.ZombieConsumerGroups(false) {
			lines = append(lines, fmt.Sprintf("%s|%s", zkcluster.Name(), zombie))
		}
	})

	if len(lines) == 1 {
		this.Ui.Info("good")
	} else {
		this.Ui.Output(columnize.SimpleFormat(lines))
	}
}

func (this *Consumers) cleanupStaleConsumerGroups(zkzone *zk.ZkZone, clusterPattern string) {
	// what consumer groups are safe to delete?
	// 1. not online
	// 2. have no offsets
	this.Ui.Output(color.Blue(zkzone.Name()))

	zkzone.ForSortedClusters(func(zkcluster *zk.ZkCluster) {
		if !patternMatched(zkcluster.Name(), clusterPattern) {
			return
		}

		this.Ui.Output(strings.Repeat(" ", 4) + zkcluster.Name())
		consumerGroups := zkcluster.ConsumerGroups()
		for group, consumers := range consumerGroups {
			if len(consumers) > 0 {
				// this consumer group is online
				continue
			}

			if !patternMatched(group, this.groupPattern) {
				continue
			}

			if !strings.HasPrefix(group, "console-consumer-") {
				path := zkcluster.ConsumerGroupOffsetPath(group)
				_, _, err := zkzone.Conn().Children(path)
				if err == nil {
					this.Ui.Warn(fmt.Sprintf("%s not empty, unsafe to cleanup", path))
					continue
				}

				if err != gozk.ErrNoNode {
					// should never happen
					swallow(err)
				}
			}

			// have no offsets, safe to delete
			if this.confirmYes {
				yes, err := this.Ui.Ask(fmt.Sprintf("confirm to remove cluster[%s] consumer group: %s? [Y/n]",
					zkcluster.Name(), group))
				swallow(err)

				if strings.ToLower(yes) == "n" {
					this.Ui.Info(fmt.Sprintf("%s skipped", group))
					continue
				}
			} else {
				yes, err := this.Ui.Ask(fmt.Sprintf("confirm to remove cluster[%s] consumer group: %s? [y/N]",
					zkcluster.Name(), group))
				swallow(err)

				if strings.ToLower(yes) != "y" {
					this.Ui.Info(fmt.Sprintf("%s skipped", group))
					continue
				}
			}

			// do delete this consumer group
			zkzone.DeleteRecursive(zkcluster.ConsumerGroupRoot(group))
			this.Ui.Info(fmt.Sprintf("%s deleted", group))
		}
	})
}

func (this *Consumers) printConsumersByHost(zkzone *zk.ZkZone, clusterPattern string) {
	outputs := make(map[string]map[string]map[string]int) // host: {cluster: {topic: count}}

	this.Ui.Output(color.Blue(zkzone.Name()))

	zkzone.ForSortedClusters(func(zkcluster *zk.ZkCluster) {
		if !patternMatched(zkcluster.Name(), clusterPattern) {
			return
		}

		consumerGroups := zkcluster.ConsumerGroups()
		for _, group := range consumerGroups {
			for _, c := range group {
				if _, present := outputs[c.Host()]; !present {
					outputs[c.Host()] = make(map[string]map[string]int)
				}

				if _, present := outputs[c.Host()][zkcluster.Name()]; !present {
					outputs[c.Host()][zkcluster.Name()] = make(map[string]int)
				}

				for topic, count := range c.Subscription {
					outputs[c.Host()][zkcluster.Name()][topic] += count
				}
			}
		}

	})

	sortedHosts := make([]string, 0, len(outputs))
	for host := range outputs {
		sortedHosts = append(sortedHosts, host)
	}
	sort.Strings(sortedHosts)
	for _, host := range sortedHosts {
		tc := outputs[host]
		this.Ui.Output(fmt.Sprintf("%s %+v", color.Green("%22s", host), tc))
	}
}

func (this *Consumers) printConsumersByGroupTable(zkzone *zk.ZkZone, clusterPattern string) {
	lines := []string{"Zone|Cluster|M|Host|ConsumerGroup|Topic/Partition|Offset|Lag|Uptime"}

	zkzone.ForSortedClusters(func(zkcluster *zk.ZkCluster) {
		kfk, err := sarama.NewClient(zkcluster.BrokerList(), sarama.NewConfig())
		swallow(err)
		defer kfk.Close()

		groupTopicsMap := make(map[string]map[string]struct{}) // group:sub topics

		if !patternMatched(zkcluster.Name(), clusterPattern) {
			return
		}

		consumerGroups := zkcluster.ConsumerGroups()
		sortedGroups := make([]string, 0, len(consumerGroups))
		for group := range consumerGroups {
			if !patternMatched(group, this.groupPattern) {
				continue
			}

			sortedGroups = append(sortedGroups, group)
		}

		sort.Strings(sortedGroups)
		for _, group := range sortedGroups {
			consumers := consumerGroups[group]
			if this.onlineOnly && len(consumers) == 0 {
				continue
			}

			if len(consumers) > 0 {
				// sort by host
				sortedIds := make([]string, 0)
				consumersMap := make(map[string]*zk.ConsumerZnode)
				for _, c := range consumers {
					sortedIds = append(sortedIds, c.Id)
					consumersMap[c.Id] = c
				}
				sort.Strings(sortedIds)

				for _, consumerId := range sortedIds {
					c := consumersMap[consumerId]
					for topic := range c.Subscription {
						if !patternMatched(topic, this.topicPattern) {
							continue
						}

						if len(groupTopicsMap[group]) == 0 {
							groupTopicsMap[group] = make(map[string]struct{}, 5)
						}
						groupTopicsMap[group][topic] = struct{}{}

						ownerByPartition := zkcluster.OwnersOfGroupByTopic(group, topic)

						partitionsWithOffset := make(map[string]struct{})
						for _, offset := range this.displayGroupOffsets(zkcluster, kfk, group, topic, false) {

							onlineSymbol := "◉"
							isOwner := false
							if ownerByPartition[offset.partitionId] == consumerId {
								onlineSymbol += "*" // owned by this consumer
								isOwner = true
							}
							if this.ownerOnly && !isOwner {
								continue
							}

							partitionsWithOffset[offset.partitionId] = struct{}{}

							lines = append(lines,
								fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%s|%s",
									zkzone.Name(), zkcluster.Name(),
									onlineSymbol,
									c.Host(),
									group+"@"+c.Id[len(c.Id)-12:],
									fmt.Sprintf("%s/%s", offset.topic, offset.partitionId),
									offset.offset, offset.lag,
									gofmt.PrettySince(c.Uptime())))
						}

						for partitionId := range ownerByPartition {
							if _, present := partitionsWithOffset[partitionId]; !present {
								// this consumer is owner online, but has no offset
								onlineSymbol := "◉"
								isOwner := false
								if ownerByPartition[partitionId] == consumerId {
									onlineSymbol += "*"
									isOwner = true
								}
								if this.ownerOnly && !isOwner {
									continue
								}

								lines = append(lines,
									fmt.Sprintf("%s|%s|%s|%s|%s|%s|?|?|%s",
										zkzone.Name(), zkcluster.Name(),
										onlineSymbol,
										c.Host(),
										group+"@"+c.Id[len(c.Id)-12:],
										fmt.Sprintf("%s/%s", topic, partitionId),
										gofmt.PrettySince(c.Uptime())))
							}
						}
					}

				}
			} else {
				// offline
				for _, offset := range this.displayGroupOffsets(zkcluster, kfk, group, "", false) {
					if !patternMatched(offset.topic, this.topicPattern) {
						continue
					}

					lines = append(lines,
						fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%s|%s",
							zkzone.Name(), zkcluster.Name(),
							"◎",
							" ",
							group, fmt.Sprintf("%s/%s", offset.topic, offset.partitionId),
							offset.offset, offset.lag, " "))
				}
			}
		}

		for group, topics := range groupTopicsMap {
			if len(topics) > 1 {
				// the same consumer group is consuming more than 1 topics
				topicsLabel := make([]string, 0, len(topics))
				for t := range topics {
					topicsLabel = append(topicsLabel, t)
				}
				this.Ui.Warn(fmt.Sprintf("%35s consuming: %+v", group, topicsLabel))
			}
		}
	})

	if !this.warnOnly {
		this.Ui.Output(columnize.SimpleFormat(lines))
	}

}

func (this *Consumers) displayGroupOffsets(zkcluster *zk.ZkCluster, kfk sarama.Client, group, topic string, echo bool) []consumerGroupOffset {
	offsetMap := zkcluster.ConsumerOffsetsOfGroup(group)
	sortedTopics := make([]string, 0, len(offsetMap))
	for t := range offsetMap {
		sortedTopics = append(sortedTopics, t)
	}
	sort.Strings(sortedTopics)

	r := make([]consumerGroupOffset, 0)

	for _, t := range sortedTopics {
		if !patternMatched(t, this.topicPattern) || (topic != "" && t != topic) {
			continue
		}

		sortedPartitionIds := make([]string, 0, len(offsetMap[t]))
		for partitionId := range offsetMap[t] {
			sortedPartitionIds = append(sortedPartitionIds, partitionId)
		}
		sort.Strings(sortedPartitionIds)

		for _, partitionId := range sortedPartitionIds {
			pid, _ := strconv.Atoi(partitionId)
			latestOffset, _ := kfk.GetOffset(t, int32(pid), sarama.OffsetNewest)

			r = append(r, consumerGroupOffset{
				topic:       t,
				partitionId: partitionId,
				offset:      gofmt.Comma(offsetMap[t][partitionId]),
				lag:         gofmt.Comma(latestOffset - offsetMap[t][partitionId]),
			})

			if echo {
				this.Ui.Output(fmt.Sprintf("\t\t%s/%s Offset:%s",
					t, partitionId, gofmt.Comma(offsetMap[t][partitionId])))
			}

		}
	}

	return r

}

func (*Consumers) Synopsis() string {
	return "Print high level consumer groups from Zookeeper"
}

func (this *Consumers) Help() string {
	help := fmt.Sprintf(`
Usage: %s consumers [options]

    %s

Options:

    -z zone

    -c cluster

    -g group name pattern

    -t topic pattern

    -zb
      Locate zombie consumer groups.

    -warn
      Only show groups that consumes multiple topics.

    -online
      Only show online consumer groups.    

    -own
      Only show consumer instances that owns partitions.

    -cleanup
      Cleanup the stale consumer groups after confirmation.

    -yes
      Work with -cleanup, input 'y' by default if confirm prompted.

    -byhost
      Display consumer groups by consumer hosts.

`, this.Cmd, this.Synopsis())
	return strings.TrimSpace(help)
}
