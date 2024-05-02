import Link from "next/link";
import styles from "../page.module.css";
import { Ubuntu_Mono } from "next/font/google";
import btnstyles from "@/components/Buttons/Buttons.module.css";
const ubuntuMono = Ubuntu_Mono({
  weight: "400",
  style: "normal",
  subsets: ["latin"],
});

export default function YamlGen() {
  return (
    <>
      <main className={ubuntuMono.className}>
        <div className={styles.mainContainer}>
          <p className={styles.mainText}>
            <b>Yaml File Generator</b>
          </p>
          <div className={styles.boxesContainer}>
            <Link
              href="yaml-gen/option"
              style={{ textDecoration: "underline", color: "white" }}
            >
              <div className={styles.smallbox}>
                <img src="/arrow.png" />
                <p className={styles.boxText}>Search</p>
              </div>
            </Link>
            <Link
              href="yaml-gen/option"
              style={{ textDecoration: "underline", color: "white" }}
            >
              <div className={styles.smallbox}>
                <img src="/arrow.png" />
                <p className={styles.boxText}>Select</p>
              </div>
            </Link>
            <Link
              href="yaml-gen/option"
              style={{ textDecoration: "underline", color: "white" }}
            >
              <div className={styles.smallbox}>
                <img src="/arrow.png" />
                <p className={styles.boxText}>Init</p>
              </div>
            </Link>
            <Link
              href="yaml-gen/option"
              style={{ textDecoration: "underline", color: "white" }}
            >
              <div className={styles.smallbox}>
                <img src="/arrow.png" />
                <p className={styles.boxText}>Confirm</p>
              </div>
            </Link>
            <Link
              href="yaml-gen/option"
              style={{ textDecoration: "underline", color: "white" }}
            >
              <div className={styles.smallbox}>
                <img src="/arrow.png" />
                <p className={styles.boxText}>Status</p>
              </div>
            </Link>
          </div>
          <div className={styles.secondBoxesContainer}>
            <Link
              href="yaml-gen/option"
              style={{ textDecoration: "underline", color: "white" }}
            >
              <div className={styles.smallbox}>
                <img src="/arrow.png" />
                <p className={styles.boxText}>Track</p>
              </div>
            </Link>
            <Link
              href="yaml-gen/option"
              style={{ textDecoration: "underline", color: "white" }}
            >
              <div className={styles.smallbox}>
                <img src="/arrow.png" />
                <p className={styles.boxText}>Cancel</p>
              </div>
            </Link>
            <Link
              href="yaml-gen/option"
              style={{ textDecoration: "underline", color: "white" }}
            >
              <div className={styles.smallbox}>
                <img src="/arrow.png" />
                <p className={styles.boxText}>Update</p>
              </div>
            </Link>
            <Link
              href="yaml-gen/option"
              style={{ textDecoration: "underline", color: "white" }}
            >
              <div className={styles.smallbox}>
                <img src="/arrow.png" />
                <p className={styles.boxText}>Rating</p>
              </div>
            </Link>
            <Link
              href="yaml-gen/option"
              style={{ textDecoration: "underline", color: "white" }}
            >
              <div className={styles.smallbox}>
                <img src="/arrow.png" />
                <p className={styles.boxText}>Support</p>
              </div>
            </Link>
          </div>
        </div>
        <div style={{ padding: "20px" }}>
          <a className={btnstyles.primaryButton} href="/yaml-gen/check-yaml">
            Check Yaml Config
          </a>
        </div>
      </main>
    </>
  );
}
