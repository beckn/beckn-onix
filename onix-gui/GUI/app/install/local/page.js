"use client";

import Link from "next/link";
import styles from "../../page.module.css";
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
          <button
            onClick={() => window.history.back()}
            className={styles.backButton}
          >
            Back
          </button>
          <p className={styles.mainText}>
            Set up a network on your local machine
          </p>
          <div className={styles.boxesContainer}>
            <Link
              href="/setup/gateway"
              style={{ textDecoration: "underline", color: "white" }}
            >
              <div className={styles.box}>
                <Image alt="arrow" width={20} height={20} src="/arrow.png" />
                <p className={styles.boxText}>Gateway</p>
              </div>
            </Link>
            <Link
              href="/setup/bap"
              style={{ textDecoration: "underline", color: "white" }}
            >
              <div className={styles.box}>
                <Image alt="arrow" width={20} height={20} src="/arrow.png" />
                <p className={styles.boxText}>BAP Adapter</p>
              </div>
            </Link>
            <Link
              href="/setup/bpp"
              style={{ textDecoration: "underline", color: "white" }}
            >
              <div className={styles.box}>
                <Image alt="arrow" width={20} height={20} src="/arrow.png" />
                <p className={styles.boxText}>BPP Adapter</p>
              </div>
            </Link>
          </div>
        </div>
      </main>
    </>
  );
}
