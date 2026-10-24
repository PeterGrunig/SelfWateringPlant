#include <WiFi.h>
#include <Wire.h>
#include "Adafruit_seesaw.h"
#include <Firebase_ESP_Client.h>
#include <Preferences.h>
#include <time.h>

// Needed for token status callback
#include "addons/TokenHelper.h"
#include "addons/RTDBHelper.h"

// ---------- PRIVATE CONFIG ----------
// Create firmware/secrets.h from secrets.example.h and keep it out of Git.
#include "secrets.h"

// How often to log soil moisture (ms). 600000UL = every 10 min.
const unsigned long SOIL_READ_INTERVAL = 600000UL;

// After this many consecutive push failures, restart the device
const int MAX_CONSECUTIVE_FAILURES = 5;
// -----------------------------

// STEMMA Soil Sensor (capacitive, I2C via seesaw chip)
// Wiring: VIN -> 3V3 (or 5V), GND -> GND, SDA -> GPIO21, SCL -> GPIO22
#define SEESAW_ADDR 0x36

// How often to log soil moisture (ms). 600000UL = every 10 min.
const unsigned long SOIL_READ_INTERVAL = 600000UL;

// After this many consecutive push failures, restart the device
const int MAX_CONSECUTIVE_FAILURES = 5;
// -----------------------------

FirebaseData fbdo;
FirebaseData fbdoErr; // separate fbdo for error logging, so it doesn't collide with the main push's connection state
FirebaseAuth auth;
FirebaseConfig config;
Preferences prefs;
Adafruit_seesaw ss;

unsigned long lastSoilRead = 0;
int consecutiveFailures = 0;

// Returns a human-readable UTC timestamp string, e.g. "2026-07-29 03:19:50"
String readableTimestamp() {
  time_t now = time(nullptr);
  struct tm timeinfo;
  gmtime_r(&now, &timeinfo);
  char buf[25];
  strftime(buf, sizeof(buf), "%Y-%m-%d %H:%M:%S", &timeinfo);
  return String(buf);
}

// Reads the capacitive touch value from the seesaw chip.
// Range is roughly 200 (very dry) to 2000 (very wet).
uint16_t readSoilMoisture() {
  return ss.touchRead(0);
}

// Bonus: the seesaw chip also exposes its onboard temperature sensor
// (+/- ~2C accuracy, but handy context for the moisture reading).
float readSoilTemperature() {
  return ss.getTemp();
}

// Persist an error locally (flash) so it survives connectivity loss / restarts.
// Stores at most a handful of recent errors as a simple newline-delimited log.
void saveErrorLocally(const String &reason) {
  String entry = readableTimestamp();
  entry += " | ";
  entry += reason;

  String existing = prefs.getString("pending_errs", "");
  if (existing.length() > 0) {
    existing += "\n";
  }
  existing += entry;

  // Cap stored size so we don't overflow NVS if errors pile up during a long outage
  const size_t MAX_STORED_CHARS = 1500;
  if (existing.length() > MAX_STORED_CHARS) {
    existing = existing.substring(existing.length() - MAX_STORED_CHARS);
  }

  prefs.putString("pending_errs", existing);
}

// Called once we have a working Firebase connection - flushes any errors
// that were saved locally during a previous outage.
void flushPendingErrorsToFirebase() {
  String pending = prefs.getString("pending_errs", "");
  if (pending.length() == 0) return;

  Serial.println("Flushing pending errors saved during outage...");

  // Each line was stored as "timestamp | reason"
  int start = 0;
  bool allSent = true;
  while (start < (int)pending.length()) {
    int nl = pending.indexOf('\n', start);
    String line = (nl == -1) ? pending.substring(start) : pending.substring(start, nl);

    int sep = line.indexOf(" | ");
    String ts = (sep == -1) ? "" : line.substring(0, sep);
    String reason = (sep == -1) ? line : line.substring(sep + 3);

    FirebaseJson json;
    json.set("reason", reason);
    json.set("timestamp", ts);
    json.set("recoveredLate", true);

    if (Firebase.RTDB.pushJSON(&fbdoErr, "/errors", &json)) {
      String msg = "Flushed: ";
      msg += line;
      Serial.println(msg);
    } else {
      String msg = "Failed to flush a pending error, will retry next time: ";
      msg += line;
      Serial.println(msg);
      allSent = false;
    }
    fbdoErr.clear();

    if (nl == -1) break;
    start = nl + 1;
  }

  // Only clear storage if everything actually made it out
  if (allSent) {
    prefs.putString("pending_errs", "");
  }
}

void logErrorToFirebase(const String &reason) {
  FirebaseJson json;
  json.set("reason", reason);
  json.set("rssi", WiFi.RSSI());
  json.set("freeHeap", (int)ESP.getFreeHeap());
  json.set("largestBlock", (int)ESP.getMaxAllocHeap());
  json.set("wifiConnected", WiFi.status() == WL_CONNECTED);
  json.set("timestamp", readableTimestamp());

  if (Firebase.RTDB.pushJSON(&fbdoErr, "/errors", &json)) {
    Serial.println("Error logged to Firebase.");
  } else {
    String msg = "Also failed to log error to Firebase, saving locally instead: ";
    msg += fbdoErr.errorReason();
    Serial.println(msg);
    saveErrorLocally(reason);
  }
  fbdoErr.clear();
}

void logSoilMoisture(uint16_t rawValue, float tempC) {
  // Guard: don't start a new push if fbdo thinks a connection is still open.
  if (fbdo.httpConnected()) {
    Serial.println("WARN: fbdo still shows an open connection. Clearing before retry.");
    fbdo.clear();
    delay(50);
  }

  FirebaseJson json;
  json.set("value", rawValue);
  json.set("tempC", tempC);
  json.set("timestamp", readableTimestamp());

  Serial.printf("RSSI: %d dBm | Heap before push: %u (largest block: %u)\n",
                WiFi.RSSI(), ESP.getFreeHeap(), ESP.getMaxAllocHeap());

  bool ok = Firebase.RTDB.pushJSON(&fbdo, "/soilMoisture", &json);

  Serial.printf("Heap after push: %u (largest block: %u)\n",
                ESP.getFreeHeap(), ESP.getMaxAllocHeap());

  if (ok) {
    Serial.printf("moisture=%u tempC=%.1f\n", rawValue, tempC);
    consecutiveFailures = 0;
    // We have a working connection right now - flush any backlog from a past outage
    flushPendingErrorsToFirebase();
  } else {
    String reason = fbdo.errorReason();
    String failMsg = "Push FAILED. Reason: ";
    failMsg += reason;
    Serial.println(failMsg);

    consecutiveFailures++;
    logErrorToFirebase(reason);

    if (WiFi.status() != WL_CONNECTED) {
      Serial.println("WiFi not connected. Attempting reconnect...");
      WiFi.reconnect();
      delay(3000);
    }

    if (consecutiveFailures >= MAX_CONSECUTIVE_FAILURES) {
      Serial.printf("Hit %d consecutive failures. Restarting device...\n", consecutiveFailures);
      String restartMsg = "Restarting after ";
      restartMsg += consecutiveFailures;
      restartMsg += " consecutive failures";
      logErrorToFirebase(restartMsg); // will also fall back to local storage if this fails
      delay(500);
      ESP.restart();
    }
  }

  fbdo.clear();
}

void setup() {
  Serial.begin(115200);
  esp_reset_reason_t reason = esp_reset_reason();
  Serial.print("Reset reason code: ");
  Serial.println(reason);

  prefs.begin("soilsensor", false); // namespace for persistent storage

  // Bring up I2C and the seesaw-based soil sensor.
  // Default ESP32 I2C pins: SDA=21, SCL=22 (adjust if your board differs).
  Wire.begin();
  if (!ss.begin(SEESAW_ADDR)) {
    Serial.println("ERROR: seesaw soil sensor not found. Check wiring/address!");
    // Keep going - Firebase error logging below will report this once connected,
    // and readSoilMoisture()/readSoilTemperature() will just return junk values
    // until the sensor is reachable.
  } else {
    Serial.print("Seesaw started! Version: ");
    Serial.println(ss.getVersion(), HEX);
  }

  // Connect to WiFi
  WiFi.begin(WIFI_SSID, WIFI_PASSWORD);
  WiFi.setSleep(false);
  Serial.print("Connecting to WiFi");
  while (WiFi.status() != WL_CONNECTED) {
    Serial.println(WiFi.status());
    Serial.print(".");
    delay(300);
  }
  Serial.println();
  Serial.print("Connected with IP: ");
  Serial.println(WiFi.localIP());
  Serial.printf("Initial RSSI: %d dBm\n", WiFi.RSSI());

  // Sync time via NTP - required for TLS certificate validation AND our readable timestamps
  configTime(0, 0, "pool.ntp.org", "time.nist.gov");
  Serial.print("Waiting for NTP time sync");
  time_t now = time(nullptr);
  unsigned long ntpStart = millis();
  while (now < 8 * 3600 * 2) { // wait until time looks valid (not epoch 0)
    delay(500);
    Serial.print(".");
    now = time(nullptr);
    if (millis() - ntpStart > 15000) { // don't hang forever if NTP is unreachable
      Serial.println("\nWARN: NTP sync timed out, continuing anyway.");
      break;
    }
  }
  Serial.println();
  Serial.print("Time synced: ");
  Serial.println(ctime(&now));

  // Firebase config
  config.api_key = API_KEY;
  config.database_url = DATABASE_URL;

  auth.user.email = USER_EMAIL;
  auth.user.password = USER_PASSWORD;

  config.token_status_callback = tokenStatusCallback;
  fbdo.setBSSLBufferSize(4096, 1024);
  fbdoErr.setBSSLBufferSize(4096, 1024);

  Firebase.begin(&config, &auth);
  Firebase.reconnectWiFi(true);

  Serial.println("Waiting for Firebase authentication...");
}

void loop() {
  if (!Firebase.ready()) return;

  if (millis() - lastSoilRead > SOIL_READ_INTERVAL || lastSoilRead == 0) {
    lastSoilRead = millis();
    Serial.print("Free heap: ");
    Serial.println(ESP.getFreeHeap());
    uint16_t soilValue = readSoilMoisture();
    float soilTempC = readSoilTemperature();
    logSoilMoisture(soilValue, soilTempC);
  }
}
