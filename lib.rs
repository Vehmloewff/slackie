pub mod auth;
pub mod config;
pub mod daemon;
pub mod db;
pub mod error;
pub mod ipc;
pub mod lists;
pub mod manifest;
pub mod read;
pub mod send;
pub mod slack;
pub mod socket;
pub mod sync;

#[cfg(test)]
mod tests {
    use crate::{
        config::Config,
        db::Db,
        manifest::manifest,
        send::{file_share_ts, split_thread},
        slack::{Conv, Msg, User},
    };
    use serde_json::json;

    #[test]
    fn manifest_sets_selected_read_scopes_and_events() {
        let config = Config {
            read_app_mentions_only: false,
            read_dms: true,
            read_private_channels: false,
            read_message_groups: true,
            ..Config::default()
        };
        let value = manifest("Test Bot", &config);
        let scopes = value["oauth_config"]["scopes"]["bot"].as_array().unwrap();
        let events = value["settings"]["event_subscriptions"]["bot_events"]
            .as_array()
            .unwrap();
        assert!(scopes.iter().any(|v| v == "im:history"));
        assert!(!scopes.iter().any(|v| v == "groups:history"));
        assert!(events.iter().any(|v| v == "message.channels"));
        assert!(events.iter().any(|v| v == "message.mpim"));
        assert_eq!(value["display_information"]["name"], "Test Bot");
    }

    #[test]
    fn thread_target_splits_only_timestamp_suffix() {
        assert_eq!(
            split_thread("#channel:1740000000.123456"),
            ("#channel", "1740000000.123456".to_owned())
        );
        assert_eq!(
            split_thread("#channel:not-a-ts"),
            ("#channel:not-a-ts", String::new())
        );
    }

    #[test]
    fn completed_upload_share_provides_thread_timestamp() {
        let response =
            json!({"files":[{"shares":{"public":{"C123":[{"ts":"1740000000.123456"}]}}}]});
        assert_eq!(file_share_ts(&response, "C123"), Some("1740000000.123456"));
    }

    #[test]
    fn config_deserialization_preserves_legacy_read_defaults() {
        let config: Config = serde_json::from_str("{}").unwrap();
        assert!(
            config.read_app_mentions_only
                && config.read_dms
                && config.read_private_channels
                && config.read_message_groups
        );
    }

    #[tokio::test]
    async fn store_roundtrip_inserts_and_detects_new_messages() {
        let db = Db::open(std::path::Path::new(":memory:")).await.unwrap();
        let user = User {
            id: "U1".into(),
            name: "alice".into(),
            real_name: "Alice".into(),
            ..Default::default()
        };
        db.upsert_user(&user).await.unwrap();
        let conv = Conv {
            id: "C1".into(),
            name: "team".into(),
            ..Default::default()
        };
        db.upsert_channel(&conv).await.unwrap();
        let msg = Msg {
            ts: "1740000000.000001".into(),
            user: "U1".into(),
            text: "hello".into(),
            ..Default::default()
        };
        assert!(db.upsert_message("C1", &msg).await.unwrap());
        // Re-inserting identical data must not count as a new message.
        assert!(!db.upsert_message("C1", &msg).await.unwrap());
        // An edited message counts as a change.
        let mut edited = msg.clone();
        edited.text = "hello!".into();
        assert!(db.upsert_message("C1", &edited).await.unwrap());

        assert_eq!(db.channel_max_ts("C1").await.unwrap(), "1740000000.000001");
        assert!(db.messages_after("").await.unwrap().len() == 1);
        assert!(db
            .messages_after("1740000000.000001")
            .await
            .unwrap()
            .is_empty());
        let found = db.messages_after("").await.unwrap();
        assert_eq!(found[0].0.id, "C1");
        assert_eq!(found[0].1.text, "hello!");
        assert_eq!(db.user("U1").await.unwrap().unwrap().name, "alice");

        db.state_set("last_seen", "1740000000.000001")
            .await
            .unwrap();
        assert_eq!(
            db.state_get("last_seen").await.unwrap().as_deref(),
            Some("1740000000.000001")
        );
    }

    #[tokio::test]
    async fn read_prints_store_messages_and_waits_for_new_ones() {
        // Point the config lookup at a throwaway config with a known cursor.
        let dir = std::env::temp_dir().join(format!("slackie-read-test-{}", std::process::id()));
        let cfg_dir = dir.join("slackie");
        std::fs::create_dir_all(&cfg_dir).unwrap();
        let config = Config {
            access_token: "xoxb-fake".into(),
            bot_user_id: "UBOT".into(),
            last_seen: "1700000000.000000".into(),
            read_app_mentions_only: false,
            ..Default::default()
        };
        std::fs::write(
            cfg_dir.join("config.json"),
            serde_json::to_vec_pretty(&config).unwrap(),
        )
        .unwrap();
        std::env::set_var("XDG_CONFIG_HOME", &dir);

        let db = Db::open(std::path::Path::new(":memory:")).await.unwrap();
        db.upsert_channel(&Conv {
            id: "C1".into(),
            name: "team".into(),
            ..Default::default()
        })
        .await
        .unwrap();
        db.upsert_user(&User {
            id: "U1".into(),
            name: "alice".into(),
            ..Default::default()
        })
        .await
        .unwrap();
        let (rev, mut rx) = tokio::sync::watch::channel(0u64);

        db.upsert_message(
            "C1",
            &Msg {
                ts: "1700000001.000000".into(),
                user: "U1".into(),
                text: "hello".into(),
                ..Default::default()
            },
        )
        .await
        .unwrap();

        let out = crate::read::read(&db, false, &mut rx).await.unwrap();
        assert!(
            out.contains("[#team] 1700000001.000000 alice: hello"),
            "{out}"
        );
        assert!(
            out.contains("threading target: #team:1700000001.000000"),
            "{out}"
        );
        assert_eq!(
            db.state_get("last_seen").await.unwrap().as_deref(),
            Some("1700000001.000000")
        );

        // A blocked read must wake up when the daemon records a new message.
        let waiter = {
            let db = db.clone();
            let mut rx = rev.subscribe();
            tokio::spawn(async move { crate::read::read(&db, true, &mut rx).await })
        };
        tokio::time::sleep(std::time::Duration::from_millis(100)).await;
        db.upsert_message(
            "C1",
            &Msg {
                ts: "1700000002.000000".into(),
                user: "U1".into(),
                text: "world".into(),
                ..Default::default()
            },
        )
        .await
        .unwrap();
        rev.send_modify(|v| *v += 1);

        let out = tokio::time::timeout(std::time::Duration::from_secs(5), waiter)
            .await
            .expect("read --wait did not wake up")
            .unwrap()
            .unwrap();
        assert!(
            out.contains("[#team] 1700000002.000000 alice: world"),
            "{out}"
        );
        assert_eq!(
            db.state_get("last_seen").await.unwrap().as_deref(),
            Some("1700000002.000000")
        );
    }
}
