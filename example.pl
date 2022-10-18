#!/usr/bin/env perl
use 5.16.1;
use AnySan;
use AnySan::Provider::Slack;
use Data::Dumper;
BEGIN {
	$AnyEvent::SlackRTM::START_URL = "http://localhost:8888/connect/C7MK19D8F";
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
