import Link from "next/link";
import styles from "./page.module.css";
import { Ubuntu_Mono } from "next/font/google";
import Image from "next/image";
const ubuntuMono = Ubuntu_Mono({
  weight: "400",
  style: "normal",
  subsets: ["latin"],
});

export default function Home() {
  return (
    <>
      <main className={ubuntuMono.className}>
        <div className={styles.mainContainer}>
          <p className={styles.mainHeading}>ONIX</p>
          <p className={styles.mainText}>
            Open Network In A Box, is a project designed to effortlessly set up
            and maintain Beckn network that is scalable, secure and easy to
            maintain.
          </p>
          <div className={styles.boxesContainer}>
            <Link
              href="/install"
              style={{ textDecoration: "underline", color: "white" }}
            >
              <div className={styles.box}>
                <Image alt="arrow" width={20} height={20} src="/arrow.png" />
                <p className={styles.boxText}>Installation Wizard</p>
              </div>
            </Link>
            {/* <Link
              href="/monitor"
              style={{ textDecoration: "underline", color: "white" }}
            >
              <div className={styles.box}>
                <Image alt="arrow" width={5} height={3} src="/arrow.png" />
                <p className={styles.boxText}>Network Monitor</p>
              </div>
            </Link> */}
            <Link
              href="/yaml-gen/"
              style={{ textDecoration: "underline", color: "white" }}
            >
              <div className={styles.box}>
                <Image alt="arrow" width={20} height={20} src="/arrow.png" />
                <p className={styles.boxText}>Layer 2 Tester </p>
              </div>
            </Link>
          </div>
        </div>
      </main>
    </>
  );
}
