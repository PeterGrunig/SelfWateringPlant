# Self Watering Plant

**A connected planter prototype that reads soil conditions, logs the data, and makes it easy to explore.**

This project combines an ESP32, a capacitive soil sensor, Firebase Realtime Database, and a small Go dashboard. The current build is focused on **sensing and data logging**. A pump and automatic watering control are planned for a later revision, after the sensor readings have been calibrated for the planter and soil.

<p align="center">
  <img src="https://github.com/PeterGrunig/SelfWateringPlant/blob/main/esp32_self_watering_pot_schematic%20%281%29.png?raw=true" alt="Wiring schematic for the ESP32 soil monitor" width="760">
</p>

## What it does today

- Reads the capacitive soil sensor and its onboard temperature sensor with an ESP32.
- Records a UTC timestamp and sends a reading to Firebase every 10 minutes.
- Displays the most recent readings in a responsive Go dashboard with a 24-hour chart.
- Logs connection failures and keeps a small error backlog in ESP32 non-volatile storage so it can retry after connectivity returns.
- Keeps Wi-Fi and Firebase credentials out of tracked source files.

The moisture value shown in the dashboard is the sensor's **raw reading**, not a calibrated percentage. Sensor output depends on the sensor, soil mix, and placement. Calibrate those values before using them to make watering decisions.

## System overview

```text
Capacitive soil sensor
          │ I²C
          ▼
        ESP32 ───── Wi-Fi ───── Firebase Realtime Database
                                      │ server-side read
                                      ▼
                                Go dashboard
```

The Go server reads Firebase and renders the readings from the server side. Firebase credentials are not sent to the visitor's browser.

## Hardware

| Part | Role |
| --- | --- |
| ESP32 development board | Reads the sensor and sends telemetry over Wi-Fi |
| STEMMA Soil Sensor with seesaw interface | Capacitive soil reading and board temperature |
| Jumper wires | Connect power, ground, SDA, and SCL |
| Pump and driver | Planned addition; not connected in the current build |

The firmware uses the ESP32's default I²C pins: SDA on GPIO 21 and SCL on GPIO 22. Check the pinout for your specific development board before wiring. The existing schematic is also available as [`esp32_self_watering_pot_schematic (1).png`](<./esp32_self_watering_pot_schematic (1).png>).

## Firmware setup

1. Install the ESP32 board support package, Adafruit seesaw library, and Firebase ESP Client library in Arduino IDE.
2. Copy `firmware/plant_monitor/secrets.example.h` to `firmware/plant_monitor/secrets.h` and fill in your own Wi-Fi and Firebase values.
3. Open `firmware/plant_monitor/plant_monitor.ino` in Arduino IDE and select your ESP32 board and serial port.
4. Upload the sketch and open Serial Monitor at 115200 baud to confirm sensor startup and Firebase connection.

`secrets.h` is ignored by Git. Do not commit it. If credentials have been exposed, replace the affected passwords and review your Firebase Realtime Database security rules before using the device again.

## Dashboard setup

The dashboard uses Go's standard library and does not require a JavaScript package install.

1. Copy `.env.example` to `.env`.
2. Set `FIREBASE_DATABASE_URL` to your Realtime Database URL. If your database requires authentication, set `FIREBASE_AUTH_TOKEN` to a valid token with read access to the configured telemetry path.
3. Start the dashboard from the repository root:

   ```sh
   go run .
   ```

4. Open `http://localhost:8080`.

Environment variables set by your host take precedence over values in `.env`. The local `.env` file is ignored by Git. A Firebase ID token can expire, so use an appropriate refreshable server-side authentication approach for a long-running deployment. Keep database rules restrictive; do not make the telemetry tree public just to make the dashboard work.

The included Dockerfile can be used to build a deployable container with `docker build -t self-watering-plant .`. Pass the same environment variables to the container when you run it. The app listens on the port provided by `PORT` (8080 by default).

### Dashboard configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `FIREBASE_DATABASE_URL` | Required | Realtime Database endpoint |
| `FIREBASE_AUTH_TOKEN` | Empty | Optional token for server-side reads |
| `TELEMETRY_SOURCE_PATH` | `soilMoisture` | Firebase path containing reading records |
| `TELEMETRY_TIMESTAMP_FIELD` | `timestamp` | Timestamp property in each record |
| `TELEMETRY_VALUE_FIELD` | `value` | Raw soil sensor property |
| `TELEMETRY_TEMPERATURE_FIELD` | `tempC` | Sensor temperature property |
| `PORT` | `8080` | Local web server port |

Each Firebase child record is expected to contain a numeric `value` and a `timestamp`. The firmware writes timestamps in UTC as `YYYY-MM-DD HH:MM:SS`; the dashboard also understands Unix timestamps in seconds or milliseconds.

## Next steps

- Add a build photo and document the assembled planter.
- Collect dry-soil and fully-watered readings to calibrate the sensor range.
- Add a pump, suitable driver, and separate power supply after confirming the electrical requirements.
- Add a watering threshold, cooldown, and pump run-time limit, then test the control logic with the pump isolated from the plant.
- Consider a water-reservoir level check and a manual override before unattended operation.

## Project status

| Area | Status |
| --- | --- |
| Soil sensing | Implemented |
| Firebase telemetry and error logging | Implemented |
| Web dashboard | Implemented in this repository |
| Pump hardware and automatic watering | Planned |
| Sensor calibration for a specific plant and soil | To be measured |
| Setup photo | To be added |

## Repository layout

```text
.
├── firmware/plant_monitor/   ESP32 firmware and safe config example
├── static/                   Dashboard styles and chart code
├── templates/                Go HTML templates
├── main.go                   Web server and Firebase reader
├── telemetry.go              Firebase record parsing and time filtering
├── envfile.go                Small local .env loader
└── .env.example              Dashboard configuration template
```
