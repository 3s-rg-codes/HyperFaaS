import fs from 'fs';
import papaparse from 'papaparse';

export function loadConfig() {
    const config = JSON.parse(fs.readFileSync('./config/config.json', 'utf8'));
    const parsedCSV = papaparse.parse(fs.readFileSync('./config/config.csv', 'utf8'), { header: true }).data;

    const payloads = parsedCSV.map((row) => {
        const payload = {};
        config.function.params.forEach((param) => {
            payload[param] = Number(row[param]) || 0;
        });

        return {
            seconds: Number(row.seconds),
            vus: Number(row.vus),
            rps: Number(row.rps),
            payload
        }
    });

    return { config, payloads };
}
