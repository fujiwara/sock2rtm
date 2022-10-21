#!/usr/bin/env perl
use 5.16.1;
use AnySan;
use AnySan::Provider::Slack;
use Data::Dumper;
BEGIN {
	#$AnyEvent::SlackRTM::START_URL = "http://localhost:8888/start/C7MK19D8F,C0ATSF1MF";
	$AnyEvent::SlackRTM::START_URL = "http://localhost:8888/start/C7MK19D8F,C02HZS4RT,C9C5GNFC3,C0UFX9UHG,C1ASUL4GL/foobar";
}

my $slack = slack(
    token => $ENV{SLACK_TOKEN},
);

AnySan->register_listener(
    slack => {
        event => 'message',
        cb => sub {
            my $r = shift;
            say sprintf("from:%s message:%s", $r->from_nickname, $r->message);
        },
    },
);
AnyEvent->condvar->recv;
