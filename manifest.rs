use crate::config::Config;
use serde_json::{json, Value};
fn scopes(s: &Config) -> (Vec<&'static str>, Vec<&'static str>) {
    let mut sc = vec![
        "channels:history",
        "channels:join",
        "channels:read",
        "chat:write",
        "files:read",
        "files:write",
        "im:write",
        "users:read",
    ];
    let mut ev = vec![];
    if s.read_app_mentions_only {
        sc.push("app_mentions:read");
        ev.push("app_mention");
    } else {
        ev.push("message.channels");
    }
    if s.read_dms {
        sc.extend(["im:history", "im:read"]);
        ev.push("message.im");
    }
    if s.read_private_channels {
        sc.extend(["groups:history", "groups:read"]);
        if !s.read_app_mentions_only {
            ev.push("message.groups");
        }
    }
    if s.read_message_groups {
        sc.extend(["mpim:history", "mpim:read"]);
        if !s.read_app_mentions_only {
            ev.push("message.mpim");
        }
    }
    (sc, ev)
}
pub(crate) fn manifest(name: &str, s: &Config) -> Value {
    let (sc, ev) = scopes(s);
    let mut features = json!({"bot_user":{"display_name":name,"always_online":false}});
    if s.read_dms {
        features["app_home"] =
            json!({"messages_tab_enabled":true,"messages_tab_read_only_enabled":false});
    }
    json!({"display_information":{"name":name},"features":features,"oauth_config":{"scopes":{"bot":sc}},"settings":{"event_subscriptions":{"bot_events":ev},"interactivity":{"is_enabled":true},"org_deploy_enabled":false,"socket_mode_enabled":true,"token_rotation_enabled":false,"is_mcp_enabled":false}})
}
